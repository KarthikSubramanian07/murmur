package daemon

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/msgsock"
	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/t3"
	"github.com/a-Fig/murmur/internal/view"
	"github.com/a-Fig/murmur/internal/web"
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
	// Messages kept over a restart never passed through addInboxLocked, which is what kicks: look at
	// them once, before waiting for anything.
	d.kickPush()
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
//
// Nothing is delivered for a user who has left (docs/VISION.md, "Joining a repo"): the daemon exits
// within ConsentPoll of the consent going, and until then a message that comes in by sync waits in
// the inbox instead of being put into a thread or a socket. Delivery is the one thing the daemon does
// to the user's other programs, so it asks here, before taking a message, and again in sendTurn,
// before each attempt.
func (d *Daemon) pusher(ctx context.Context, id string) {
	for {
		if !d.consented() {
			d.mu.Lock()
			delete(d.pushing, id)
			d.mu.Unlock()
			return
		}
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
// page says never, for the device or for this session. "Within N minutes" delivers as "always"
// does: a message is delivered at once, as Tyler decided on 2026-10-29.
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
		if m.Pushing {
			// One at a time, so they arrive in order. A message on its way in that isn't this
			// session's pusher's came with the inbox of a session this one replaced (see
			// takeInboxLocked): its pusher kicks when it is done.
			return nil, "", proto.Route{}
		}
		if next == nil && pushable(m) {
			next = m
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

// pushable says a message may still be delivered as a turn: it wasn't told to the session by the
// hook, isn't on its way in already, hasn't failed, wasn't handed over before (Attempted: not even
// by a daemon that has since restarted), and isn't too old.
func pushable(m *inMsg) bool {
	return !m.Noticed && !m.Pushing && !m.PushFailed && !m.Attempted && time.Since(m.Arrived) <= pushMaxAge
}

// queuedForPushLocked says message m of session id is on its way in as a turn, or will be: the
// hook's notice leaves it alone, as it would be a second telling of what the turn says, and
// would take the message out of the delivery for good. Caller holds mu.
func (d *Daemon) queuedForPushLocked(id string, m *inMsg, wake pageSettings) bool {
	if m.Pushing {
		return true
	}
	if !pushable(m) || !wake.wakes(d.sessionNameLocked(id)) {
		return false
	}
	s := d.sessions[id]
	return viaOf(d.routeOfLocked(id), s != nil && s.dropped.IsZero()) != ""
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

// deliver puts the message into the session, once. It is tried a second time, after PushRetry, only
// if the first attempt never reached the way in (notReached): `t3 thread send` takes no key that
// would make a repeat harmless, and the socket answers nothing, so a call that failed after it
// started may have delivered the message, and a second one would make a second turn of it. A message
// whose delivery failed that way is left to the hook's notice, which the agent still gets, and stays
// so over a daemon restart (sendTurn marks it Attempted, and saves that, before the handoff).
func (d *Daemon) deliver(ctx context.Context, j *pushJob) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			d.log.Printf("session %s: nothing was sent to deliver a message through %s (%v); trying once more in %s",
				j.addr, j.via, err, d.opt.PushRetry)
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
		if err == nil || errors.Is(err, ErrNoConsent) || !notReached(err) {
			return err
		}
	}
	return err
}

// notReached says an attempt failed before it could have put the message anywhere: the command
// didn't start, or the socket couldn't be connected to.
func notReached(err error) bool {
	return errors.Is(err, t3.ErrNotStarted) || errors.Is(err, msgsock.ErrNotConnected)
}

// sendTurn makes one attempt, by the route's way in, unless the user has left. No lock is held: it
// starts a process or writes to a pipe.
func (d *Daemon) sendTurn(ctx context.Context, j *pushJob) error {
	if !d.consented() {
		return ErrNoConsent
	}
	switch j.via {
	case viaT3:
		return d.attempt(j.msg, func() error {
			return t3.Send(ctx, j.route.T3CLI, j.route.T3Home, j.route.T3Thread, j.text)
		})
	case viaSocket:
		return d.attempt(j.msg, func() error {
			return msgsock.Send(ctx, j.route.MsgSocket, j.route.MsgToken, j.text)
		})
	}
	return fmt.Errorf("no way to deliver by %s", j.via)
}

// attempt runs send, the handoff of message m, after marking m Attempted and saving the mark: the
// daemon may stop at any point of the call, and the next one has to know the message may be there.
// If the mark can't be saved nothing is sent, and the message is left to the hook's notice, as after
// any failed delivery. The mark comes off again if send never reached the way in (notReached), as
// nothing was sent. The save is to the file, which a restart of the process finds; a power cut may
// lose it.
func (d *Daemon) attempt(m *inMsg, send func() error) error {
	if err := d.markAttempted(m, true); err != nil {
		return err
	}
	err := send()
	if notReached(err) {
		d.markAttempted(m, false)
	}
	return err
}

// markAttempted sets m.Attempted to v and saves it. A mark that couldn't be saved is not kept, and
// is an error; a mark that couldn't be taken off is logged, and left in the file, which errs the
// safe way (a notice after a restart).
func (d *Daemon) markAttempted(m *inMsg, v bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	m.Attempted = v
	err := writeJSON(d.statePath(), d.st)
	switch {
	case err == nil:
	case v:
		m.Attempted = false
		return fmt.Errorf("not delivering the message, as the mark that it was handed over can't be saved: %w", err)
	default:
		d.log.Printf("saving state: %v", err)
	}
	return nil
}

// finishPush records how a delivery went. A delivered message leaves the inbox, wherever it is by
// now (a session that takes a name takes the inboxes kept for it); one that failed is the hook's
// again, and the first failure for a session is logged.
func (d *Daemon) finishPush(j *pushJob, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	j.msg.Pushing = false
	if errors.Is(err, ErrNoConsent) {
		return // the user left on its way: it waits in the inbox, undelivered and not a failure
	}
	d.kickPush() // what waited behind this message, in an inbox that took it over, may go now
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
	d.noteConversationLocked() // the agent now has the message; its answer may soon follow
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
	d.retireRoutesLocked(s, want)
	if d.st.T3Routes[s.id] != want {
		d.st.T3Routes[s.id] = want
		d.saveLocked()
	}
}

// retireRoutesLocked takes away the way into thread want from every other session that has it
// and isn't running: a thread's MCP server is a new session, with an ID of its own, each time T3
// restarts it (after 30 idle minutes) or a connection is made again, and the one it replaces, in
// its grace or stopped, still holds the thread. A message for everyone, or for the device, goes
// into both inboxes, and would be delivered to the one thread twice, or, from the new session,
// back to itself. What the old session had unread goes to the new one, which has the thread: with
// its route gone it could no longer be delivered, and a session in its grace is not one whose
// inbox the name's next holder adopts (see adoptLocked). Caller holds mu.
func (d *Daemon) retireRoutesLocked(s *session, want t3Route) {
	for id, r := range d.st.T3Routes {
		if id == s.id || r.Thread != want.Thread || r.Home != want.Home {
			continue
		}
		if o := d.sessions[id]; o == nil || !o.dropped.IsZero() {
			delete(d.st.T3Routes, id)
			if o != nil {
				o.route = proto.Route{}
			}
			d.takeInboxLocked(s, id)
			d.saveLocked()
		}
	}
}

// takeInboxLocked moves the messages session from has unread into s's inbox, in the order they
// arrived. A message s has a copy of (one for everyone is in both inboxes) stays once, as s's copy,
// unless from's is on its way in as a turn: then that one stays, so it is delivered once and is
// removed from wherever it is when it has been. A message s never needs from the other session
// stays behind (see hadCopyLocked). Caller holds mu.
func (d *Daemon) takeInboxLocked(s *session, from string) {
	old := d.st.Inbox[from]
	if len(old) == 0 {
		return
	}
	box := d.st.Inbox[s.id]
	at := map[string]int{}
	for i, m := range box {
		at[m.Event.ID] = i
	}
	for _, m := range old {
		if i, ok := at[m.Event.ID]; ok {
			if m.Pushing {
				box[i] = m
			}
			continue
		}
		if !m.Pushing && d.hadCopyLocked(s, m) {
			continue
		}
		at[m.Event.ID] = len(box)
		box = append(box, m)
	}
	sort.SliceStable(box, func(i, j int) bool { return box[i].Arrived.Before(box[j].Arrived) })
	if len(box) > maxInbox {
		box = box[len(box)-maxInbox:]
	}
	delete(d.st.Inbox, from)
	if len(box) == 0 {
		delete(d.st.Inbox, s.id)
	} else {
		d.st.Inbox[s.id] = box
	}
	d.log.Printf("session %s, taking over the T3 thread of %s, took what it had unread", bus.Addr(d.device, s.id), bus.Addr(d.device, from))
	d.kickPush()
}

// hadCopyLocked says message m, in the inbox of a session that s replaces, is not for s to take:
// s sent it itself (a message for everyone goes to every session but its sender, and until s found
// its thread the other session was another one), or it came while s was running and was for s too,
// so s was given a copy of its own, which s may have read since: its copy would not be in its inbox
// to be told from this one. Caller holds mu.
func (d *Daemon) hadCopyLocked(s *session, m *inMsg) bool {
	if m.Event.From == bus.Addr(d.device, s.id) {
		return true
	}
	return !m.Arrived.Before(s.started) && d.addressedLocked(m.Event, s.id)
}

// setRouteLocked takes what a session's MCP server has learned about reaching its client since it
// registered: the T3 thread, once it finds it, or its client's own thread ID (Codex's). The session's name follows it from then on
// (see rekeyLocked). Caller holds mu.
func (d *Daemon) setRouteLocked(s *session, r proto.Route) {
	if titleSource(r) != titleSource(s.route) && s.title != "" {
		// Another conversation: the title of the last one must not stay if this one's can't be read.
		s.title = ""
		d.kickPresence()
	}
	s.route = r
	d.rememberRouteLocked(s)
	if key := d.uniqueKeyLocked(nameKey(r.T3Thread, s.claudeSession, r.Native, s.id), s.id); key != s.key {
		d.rekeyLocked(s, key)
	}
	d.kickPush()
}

// rekeyLocked moves s to another name key: its client's session ID, its Codex thread or its T3
// thread became known.
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
		d.noteLocked(time.Now(), fmt.Sprintf("%s found its thread: %s -> %s", addr, from, s.name))
		d.log.Printf("session %s found its thread; its name is %s now, not %s", addr, s.name, from)
	} else {
		d.log.Printf("session %s found its thread", addr)
	}
}
