package daemon

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/a-Fig/t4/internal/bus"
	"github.com/a-Fig/t4/internal/proto"
	"github.com/a-Fig/t4/internal/t3"
	"github.com/a-Fig/t4/internal/view"
	"github.com/a-Fig/t4/internal/web"
)

// Delivering a message as a turn (#57). A message that arrives for a session is put into the
// session's own conversation at once, as the next thing it is asked to do, the way a message
// between T3 threads is, so an idle agent starts working on it and nobody has to type. Where that
// can't be done the message waits for the hook's notice and `inbox`, as it did before.
//
// There are two ways in, by what the session's MCP server found out about its client (proto.Route):
//   - inside T3, `t3 thread send <thread>`: an idle thread starts a turn, a busy one takes the
//     message as its next turn, nothing is interrupted, and it works for a thread whose MCP server
//     T3 has stopped (idle threads lose theirs after 30 minutes), which is the thread most worth
//     waking;
//   - in Claude Code outside T3, the session's messaging socket (package msgsock).
//
// A delivered message is removed from the inbox, which is how it is marked read: neither the hook
// nor `inbox` shows it again. docs/VISION.md ("Delivering a message as a turn") has the reasons.

const (
	// maxPushes is how many deliveries run at once. Each `t3 thread send` starts a process, and a
	// broadcast reaches every session on the device.
	maxPushes = 4
	// pushMaxAge is how old a message may be and still wake a session: one that waited longer than
	// this (T3 was closed, a session was away) is left to the hook's notice, not sprung on the
	// agent as a turn.
	pushMaxAge = 15 * time.Minute
)

// How a message goes into a session.
const (
	viaT3     = "T3"
	viaSocket = "the messaging socket"
)

// kickPush wakes pushLoop, which finds messages to deliver. Any lock may be held.
func (d *Daemon) kickPush() {
	select {
	case d.pushKick <- struct{}{}:
	default:
	}
}

// pushLoop starts a delivery goroutine for each session that has a message to deliver and none
// running. A session's messages go one at a time, in the order they arrived; sessions go in
// parallel.
func (d *Daemon) pushLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.pushKick:
		}
		wake := d.wakeSnapshot()
		d.mu.Lock()
		for _, id := range sortedKeys(d.st.Inbox) {
			if d.pushing[id] || ctx.Err() != nil {
				continue
			}
			if m, _, _ := d.candidateLocked(id, wake); m == nil {
				continue
			}
			d.pushing[id] = true
			d.bg.Add(1) // pushLoop is itself in bg, so this never races bg.Wait
			go func() {
				defer d.bg.Done()
				d.pusher(ctx, id)
			}()
		}
		d.mu.Unlock()
	}
}

// pusher delivers session id's messages until it has none left to deliver.
func (d *Daemon) pusher(ctx context.Context, id string) {
	for {
		wake := d.wakeSnapshot()
		d.mu.Lock()
		job := d.nextPushLocked(id, wake)
		d.mu.Unlock()
		if job == nil || ctx.Err() != nil {
			return
		}
		d.finishPush(job, d.deliver(ctx, job))
	}
}

// pushJob is one message on its way into a session.
type pushJob struct {
	id, addr string
	msg      *inMsg
	via      string
	route    proto.Route
	text     string
}

// wakeSnapshot copies the page's wake settings. setMu is taken alone, never with mu held.
func (d *Daemon) wakeSnapshot() pageSettings {
	d.setMu.Lock()
	defer d.setMu.Unlock()
	return pageSettings{Wake: d.settings.Wake, SessionWake: maps.Clone(d.settings.SessionWake)}
}

// wakes says whether messages for the session called name may be delivered as turns: unless the
// page says never, for the device or for this session. "Within N minutes" waits on a session's
// last-active time (#21), which isn't recorded yet, so it delivers as "always" does.
func (p pageSettings) wakes(name string) bool {
	w := p.Wake
	if o, ok := p.SessionWake[name]; ok && name != "" {
		w = o
	}
	return w.When != web.WakeNever
}

// routeOfLocked is how a message gets into session id: what its MCP server told the daemon if it
// is registered, else, for a session of T3 that has stopped, where its thread is. Caller holds mu.
func (d *Daemon) routeOfLocked(id string) proto.Route {
	if s := d.sessions[id]; s != nil {
		return s.route
	}
	if r, ok := d.st.T3Routes[id]; ok {
		return proto.Route{T3CLI: r.CLI, T3Home: r.Home, T3Thread: r.Thread}
	}
	return proto.Route{}
}

// viaOf is the way to deliver into a session with route r, "" if there is none. A session in T3
// is reached through T3 only: its messaging socket is T3's to use, and T3 may not take a turn it
// didn't start. live says the session's MCP server is connected, which the socket needs, as the
// token dies with the server's process.
func viaOf(r proto.Route, live bool) string {
	switch {
	case r.T3CLI != "" && r.T3Thread != "":
		return viaT3
	case r.T3CLI == "" && live && r.MsgSocket != "" && r.MsgToken != "":
		return viaSocket
	}
	return ""
}

// candidateLocked finds the next message to deliver into session id, with how and where, or nil.
// A message that is being delivered, that the hook has told the session about, whose delivery
// failed, or that is too old, is passed over, and so is every message of a session the page says
// not to wake. A session with a message and no way in is logged, once. Caller holds mu.
func (d *Daemon) candidateLocked(id string, wake pageSettings) (*inMsg, string, proto.Route) {
	var next *inMsg
	for _, m := range d.st.Inbox[id] {
		if !m.Noticed && !m.Pushing && !m.PushFailed && time.Since(m.Arrived) <= pushMaxAge {
			next = m
			break
		}
	}
	if next == nil {
		return nil, "", proto.Route{}
	}
	name := d.sessionNameLocked(id)
	if !wake.wakes(name) {
		return nil, "", proto.Route{}
	}
	s := d.sessions[id]
	route := d.routeOfLocked(id)
	via := viaOf(route, s != nil && s.dropped.IsZero())
	if via == "" {
		if s != nil && !d.pushWarned[id] {
			d.pushWarned[id] = true
			d.log.Printf("session %s has no way to take a message as a turn (%s); its messages show as notices through the hook",
				bus.Addr(d.device, id), noWay(route, s.dropped.IsZero()))
		}
		return nil, "", proto.Route{}
	}
	return next, via, route
}

// noWay says why a route can't be delivered into, without anything of it that is secret.
func noWay(r proto.Route, live bool) string {
	switch {
	case r.T3CLI != "":
		return "inside T3, but its thread isn't known"
	case r.MsgSocket == "" || r.MsgToken == "":
		return "not inside T3, and no messaging socket"
	case !live:
		return "its MCP server has gone"
	}
	return "no route"
}

// sessionNameLocked is the name session id has, or last had if it has stopped. Caller holds mu.
func (d *Daemon) sessionNameLocked(id string) string {
	if s := d.sessions[id]; s != nil {
		return s.name
	}
	return d.st.SessionNames[id]
}

// nextPushLocked takes the next message for session id and marks it as being delivered, or, if
// there is none, ends the session's delivery goroutine. Caller holds mu.
func (d *Daemon) nextPushLocked(id string, wake pageSettings) *pushJob {
	m, via, route := d.candidateLocked(id, wake)
	if m == nil {
		delete(d.pushing, id)
		return nil
	}
	m.Pushing = true
	views := d.messageViewsLocked([]*inMsg{m}, true)
	return &pushJob{id: id, addr: bus.Addr(d.device, id), msg: m, via: via, route: route, text: view.Turn(views[0])}
}

// deliver puts the message into the session, once and, if that fails, once more after PushRetry.
func (d *Daemon) deliver(ctx context.Context, j *pushJob) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(d.opt.PushRetry):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		select {
		case d.pushSem <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		err = d.sendTurn(ctx, j)
		<-d.pushSem
		if err == nil {
			return nil
		}
	}
	return err
}

// sendTurn makes one attempt, by the route's way in. No lock is held: it starts a process or writes to
// a pipe.
func (d *Daemon) sendTurn(ctx context.Context, j *pushJob) error {
	switch j.via {
	case viaT3:
		return t3.Send(ctx, j.route.T3CLI, j.route.T3Home, j.route.T3Thread, j.text)
	}
	return fmt.Errorf("no way to deliver by %s", j.via)
}

// finishPush records how a delivery went. A delivered message leaves the inbox, wherever it is by
// now (a session that takes a name takes the inboxes kept for it); one that failed is the hook's
// again, and the first failure for a session is logged.
func (d *Daemon) finishPush(j *pushJob, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	j.msg.Pushing = false
	if err != nil {
		j.msg.PushFailed = true
		if !d.pushWarned[j.id] {
			d.pushWarned[j.id] = true
			d.log.Printf("session %s: delivering a message as a turn through %s failed (%v); its messages show as notices through the hook instead",
				j.addr, j.via, err)
		}
		return
	}
	for id, box := range d.st.Inbox {
		for i, m := range box {
			if m == j.msg {
				if box = append(box[:i:i], box[i+1:]...); len(box) == 0 {
					delete(d.st.Inbox, id)
				} else {
					d.st.Inbox[id] = box
				}
				break
			}
		}
	}
	d.saveLocked()
	delete(d.pushWarned, j.id)
	e := j.msg.Event
	d.noteLocked(time.Now(), fmt.Sprintf("delivered %q from %s to %s as a turn, through %s", e.Subject, e.From, j.addr, j.via))
	d.log.Printf("delivered message %s to session %s as a turn, through %s", e.ID, j.addr, j.via)
}

// rememberRouteLocked keeps where a session in T3 is, so a message for it still reaches the
// thread after its MCP server stops. Caller holds mu.
func (d *Daemon) rememberRouteLocked(s *session) {
	r := s.route
	if r.T3CLI == "" || r.T3Thread == "" {
		return
	}
	want := t3Route{CLI: r.T3CLI, Home: r.T3Home, Thread: r.T3Thread}
	if d.st.T3Routes[s.id] != want {
		d.st.T3Routes[s.id] = want
		d.saveLocked()
	}
}

// setRouteLocked takes what a session's MCP server has learned about reaching its client since it
// registered: the T3 thread, once it finds it. The session's name follows the thread from then on
// (see rekeyLocked). Caller holds mu.
func (d *Daemon) setRouteLocked(s *session, r proto.Route) {
	s.route = r
	d.rememberRouteLocked(s)
	if key := d.uniqueKeyLocked(nameKey(r.T3Thread, s.claudeSession, s.id), s.id); key != s.key {
		d.rekeyLocked(s, key)
	}
	d.kickPush()
}

// rekeyLocked moves s to another name key: its client's session ID or its T3 thread became known.
// A key that has never had a name takes the one s has, so the name doesn't change under whoever
// has just learned it; a key that has one gives s that, which is the point of keying on the
// thread: the name follows the thread across /clear, restarts and account switches. Nothing goes
// on the bus. Caller holds mu.
func (d *Daemon) rekeyLocked(s *session, key string) {
	from := s.name
	s.key = key
	if d.st.NameKeys[key] == "" && s.name != "" {
		d.st.NameKeys[key] = s.name
		d.saveLocked()
	}
	d.setNameLocked(s, d.st.NameKeys[key])
	if s.name == "" {
		d.kickNames()
	}
	d.kickPresence()
	addr := bus.Addr(d.device, s.id)
	if s.name != from {
		d.noteLocked(time.Now(), fmt.Sprintf("%s found its T3 thread: %s -> %s", addr, from, s.name))
		d.log.Printf("session %s found its T3 thread; its name is %s now, not %s", addr, s.name, from)
	} else {
		d.log.Printf("session %s found its T3 thread", addr)
	}
}
