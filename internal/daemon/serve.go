package daemon

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/a-Fig/t4/internal/bus"
	"github.com/a-Fig/t4/internal/names"
	"github.com/a-Fig/t4/internal/proto"
	"github.com/a-Fig/t4/internal/scrub"
)

const maxBody = bus.MaxBody

func fail(msg string) proto.Response { return proto.Response{Error: msg} }

func (d *Daemon) serve(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			// A daemon nobody can reach would still hold the clone's lock, so no replacement
			// could start. Exit instead; the next client starts a new one.
			d.log.Printf("accepting connections: %v; exiting", err)
			d.stop()
			return
		}
		d.bg.Add(1)
		go func() {
			defer d.bg.Done()
			d.handleConn(ctx, conn)
		}()
	}
}

func (d *Daemon) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(conn)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return
	}
	var req proto.Request
	if err := json.Unmarshal(line, &req); err != nil {
		writeResp(conn, fail("bad request: "+err.Error()))
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(d.token)) != 1 {
		writeResp(conn, fail("bad token"))
		return
	}
	conn.SetReadDeadline(time.Time{})
	if req.Op == proto.OpHello {
		d.hello(ctx, conn, r, req.Hello)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	resp := d.handle(ctx, req)
	resp.ClaudeSession = d.claudeSessionOf(req.Session)
	writeResp(conn, resp)
}

// claudeSessionOf is the Claude Code session ID held for the session id, which an MCP server
// asks for with every call: a hook may have corrected it since the server started. "" for the CLI,
// the hook, and a session that isn't registered.
func (d *Daemon) claudeSessionOf(id string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s := d.sessions[id]; s != nil {
		return s.claudeSession
	}
	return ""
}

func writeResp(conn net.Conn, resp proto.Response) {
	resp.OK = resp.OK || resp.Error == ""
	b, _ := json.Marshal(resp)
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	conn.Write(append(b, '\n'))
}

// hello registers a session and holds its connection open until the session's process exits.
// The connection closing starts the grace: the session's MCP server restarts while its
// conversation carries on, so the session stays registered, with its inbox, for SessionGrace, and
// a hello with the same ID in that time takes it over without anything going on the bus. Only
// when the grace runs out is the session stopped. Its inbox is then kept for StoppedInbox, for a
// session that comes back.
func (d *Daemon) hello(ctx context.Context, conn net.Conn, r *bufio.Reader, h *proto.Hello) {
	if h == nil || !validName(h.ID) || h.ID == proto.CLISession {
		writeResp(conn, fail("hello needs a session ID of lowercase letters, digits and dashes"))
		return
	}
	agent := bus.OneLine(h.Agent, 40)
	if agent == "" {
		agent = "unknown"
	}
	branch := branchOf(ctx, h.Cwd)
	worktree := worktreeOf(ctx, h.Cwd)
	d.mu.Lock()
	s := d.sessions[h.ID]
	if s != nil && s.dropped.IsZero() {
		d.mu.Unlock()
		writeResp(conn, fail("session ID "+h.ID+" is in use"))
		return
	}
	// A session in its grace, taken over by a server of the same process, keeps the Claude ID the
	// daemon has for it: the server may be a new one, started with the ID its process had at first,
	// which a /clear or a /resume since has made stale. The daemon's is never older. It came from a
	// hook, or from a hello, which carries the environment's ID or one the daemon gave out, and
	// a hook may have moved it since: after a daemon restart, a hello with the live ID is followed by
	// a takeover with the stale one. A server of another process is trusted.
	claude := h.ClaudeSession
	if s != nil && h.ClientPID != 0 && h.ClientPID == s.clientPID && s.claudeSession != "" {
		claude = s.claudeSession
	}
	// Two live sessions of one client session (two MCP servers in it) would share a name, and
	// a name is an address. The later one is a session of its own.
	key := d.uniqueKeyLocked(nameKey(claude, h.ID), h.ID)
	taken := false
	if s != nil {
		// A session in its grace: this connection takes it over, with its inbox and its unread
		// notices. Everyone else was never told it was gone.
		taken = true
		if s.agent != agent || s.branch != branch || s.key != key {
			d.kickPresence() // what presence shows changed; otherwise nothing is written
		}
		s.agent, s.cwd, s.claudeSession, s.branch, s.reconnect = agent, h.Cwd, claude, branch, h.Reconnect
		s.clientPID = h.ClientPID
		s.worktree = worktree
		if s.key != key {
			s.key = key
			d.setNameLocked(s, d.st.NameKeys[key])
		}
		s.dropped = time.Time{}
		close(s.takeover)
		s.takeover = nil
	} else {
		s = d.newSessionLocked(h, agent, branch, key)
		s.worktree = worktree
	}
	d.lastActive = time.Now()
	if h.Reconnect {
		d.catchUpLocked(h.ID)
	}
	name, live := s.name, s.claudeSession
	d.mu.Unlock()
	if name == "" {
		d.kickNames()
	}

	addr := bus.Addr(d.device, h.ID)
	reply := proto.Response{OK: true, Self: addr, Name: name, NamePending: name == "", ClaudeSession: live}
	if taken {
		d.log.Printf("session %s reconnected", addr)
		writeResp(conn, reply)
	} else {
		d.log.Printf("session %s started (%s, %s)", addr, agent, branch)
		writeResp(conn, reply)
		d.kickPresence()
		d.appendSoon(bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.SessionStarted, From: addr,
			At: time.Now().UTC(), Agent: agent, Branch: branch})
	}

	stopWatch := context.AfterFunc(ctx, func() { conn.Close() })
	io.Copy(io.Discard, r) // returns when the session's process closes the connection, or dies
	stopWatch()

	d.mu.Lock()
	d.lastActive = time.Now()
	if ctx.Err() != nil {
		// The daemon is stopping. The session may reconnect to the next one, so its inbox is
		// kept; the goodbye deletes this device's presence.
		delete(d.sessions, h.ID)
		d.mu.Unlock()
		return
	}
	takeover := make(chan struct{})
	s.dropped, s.takeover = time.Now(), takeover
	d.mu.Unlock()

	grace := time.NewTimer(d.opt.SessionGrace)
	defer grace.Stop()
	select {
	case <-takeover:
		return // a new connection carries the session on
	case <-ctx.Done():
		d.mu.Lock()
		if s.takeover == takeover {
			delete(d.sessions, h.ID)
		}
		d.mu.Unlock()
		return
	case <-grace.C:
	}

	d.mu.Lock()
	if s.takeover != takeover {
		d.mu.Unlock() // taken over just as the grace ran out
		return
	}
	delete(d.sessions, h.ID)
	d.lastActive = time.Now()
	d.st.Stopped[h.ID] = time.Now().Round(0)
	d.saveLocked()
	d.mu.Unlock()
	d.log.Printf("session %s stopped", addr)
	d.kickPresence()
	d.appendSoon(bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.SessionStopped, From: addr,
		At: time.Now().UTC()})
}

// liveLocked says whether session id is connected: the CLI always is, a session in its grace or
// stopped is not. Caller holds mu.
func (d *Daemon) liveLocked(id string) bool {
	if id == proto.CLISession {
		return true
	}
	s := d.sessions[id]
	return s != nil && s.dropped.IsZero()
}

func (d *Daemon) handle(ctx context.Context, req proto.Request) proto.Response {
	sess := req.Session
	if sess == "" {
		sess = proto.CLISession
	}
	d.mu.Lock()
	d.lastActive = time.Now()
	registered := d.liveLocked(sess)
	d.mu.Unlock()
	if !registered && req.Op != proto.OpNotices && req.Op != proto.OpPing {
		return fail("this session isn't registered with the daemon (it may have just restarted); try again in a few seconds")
	}
	from := bus.Addr(d.device, sess)

	switch req.Op {
	case proto.OpPing:
		return proto.Response{OK: true, Self: from}

	case proto.OpWho:
		d.mu.Lock()
		defer d.mu.Unlock()
		resp := proto.Response{OK: true, Self: from, Devices: d.whoLocked(sess)}
		if s := d.sessions[sess]; s != nil {
			resp.Name, resp.NamePending = s.name, s.name == ""
		}
		return resp

	case proto.OpStatus:
		doing := bus.OneLine(req.Doing, 120)
		if err := scrub.Check(doing); err != nil {
			return fail(err.Error())
		}
		d.mu.Lock()
		s := d.sessions[sess]
		if s != nil {
			s.doing = doing
		}
		d.mu.Unlock()
		if s == nil {
			return proto.Response{OK: true, Self: from, Note: "the CLI has no status line"}
		}
		d.kickPresence()
		return proto.Response{OK: true, Self: from, Note: "status set"}

	case proto.OpSend:
		return d.send(ctx, from, req)

	case proto.OpInbox:
		d.mu.Lock()
		defer d.mu.Unlock()
		msgs := d.messageViewsLocked(d.st.Inbox[sess], true)
		delete(d.st.Inbox, sess)
		d.saveLocked()
		return proto.Response{OK: true, Self: from, Messages: msgs}

	case proto.OpNotices:
		return d.notices(req)

	case proto.OpLog:
		limit := req.Limit
		if limit <= 0 {
			limit = 30
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		events := d.recent
		if len(events) > limit {
			events = events[len(events)-limit:]
		}
		return proto.Response{OK: true, Self: from, Events: append([]proto.EventView(nil), events...)}

	case proto.OpStop:
		d.log.Printf("stop requested")
		d.stop()
		return proto.Response{OK: true, Note: "daemon stopping"}
	}
	return fail("unknown op " + req.Op)
}

// notices returns the messages of a session it hasn't been told about yet, without bodies, and
// marks them told. The hook calls it; the hook knows only Claude Code's session ID, and its
// process: with them, the sessions of that process follow the hook to the ID it reports (see
// hookSessionsLocked), and a hook that matches nothing gets nothing.
func (d *Daemon) notices(req proto.Request) proto.Response {
	d.mu.Lock()
	defer d.mu.Unlock()
	var ids []string
	if req.ClaudeSession != "" {
		for _, s := range d.hookSessionsLocked(req.ClaudeSession, req.ClientPID) {
			ids = append(ids, s.id)
		}
	} else {
		ids = []string{req.Session}
	}
	var self string
	var fresh []*inMsg
	for _, id := range ids {
		// A session in its grace, or stopped, is told of its notices once it is back.
		if id == "" || !d.liveLocked(id) {
			continue
		}
		if self == "" {
			self = bus.Addr(d.device, id)
		}
		for _, m := range d.st.Inbox[id] {
			if !m.Noticed {
				m.Noticed = true
				fresh = append(fresh, m)
			}
		}
	}
	if self == "" {
		return proto.Response{OK: true}
	}
	if len(fresh) > 0 {
		d.saveLocked()
	}
	return proto.Response{OK: true, Self: self, Messages: d.messageViewsLocked(fresh, false)}
}

func (d *Daemon) send(ctx context.Context, from string, req proto.Request) proto.Response {
	to := strings.TrimSpace(req.To)
	subject := bus.OneLine(req.Subject, 120)
	if !bus.ValidTo(to) || subject == "" {
		return fail("a message needs a recipient (a session's name, device/session, a device, or * for everyone; see who) and a subject")
	}
	if len(req.Body) > maxBody {
		return fail(fmt.Sprintf("not sent: the body is over %d KB, and the bus keeps everything forever", maxBody>>10))
	}
	if err := scrub.Check(subject, req.Body); err != nil {
		return fail(err.Error())
	}
	// A name is an address: it is turned into the session's device/session here, and that is what
	// goes on the bus, so a daemon that doesn't know names delivers the message all the same.
	var toName, renamed string
	if names.Valid(to) {
		d.mu.Lock()
		addr, ok := d.resolveNameLocked(to)
		_, isDevice := d.peers[to]
		isDevice = isDevice || to == d.device // a device may have an ID shaped like a name
		var now string
		if ok {
			now, _, _ = d.sessionInfoLocked(addr)
		}
		d.mu.Unlock()
		switch {
		case ok:
			// The registry keeps a name for good, and the session it was first given to survives a
			// /clear or a /resume under a new name, so a name nobody holds now can still be that
			// session's address. The message goes there, and the sender is told what to call it.
			if now != "" && now != to {
				renamed = fmt.Sprintf("%s was this session's name before it moved to another conversation; it is called %s now. The message was sent to it.", to, now)
			}
			to, toName = addr, to
		case !isDevice:
			return fail(fmt.Sprintf("no session is named %q. who shows the names in use.", to))
		}
	}
	var note string
	if to != "*" {
		dev, sess := bus.SplitAddr(to)
		d.mu.Lock()
		known := d.addressKnownLocked(dev, sess)
		_, stopped := d.st.Stopped[sess]
		d.mu.Unlock()
		switch {
		case known:
		case dev == d.device && stopped:
			note = to + " has stopped. The message waits for it, in case it comes back."
		default:
			note = to + " isn't in any device's presence right now, so nobody may read this. Check `who` for addresses."
		}
	}
	e := bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message, From: from, At: time.Now().UTC(),
		To: to, Subject: subject, Body: req.Body}
	if err := d.appendEvent(ctx, e); err != nil {
		return fail("not sent: " + err.Error())
	}
	note = strings.TrimSpace(renamed + " " + note)
	return proto.Response{OK: true, Self: from, Note: note,
		Messages: []proto.MessageView{{ID: e.ID, From: from, To: to, ToName: toName, Subject: subject, At: e.At}}}
}

func (d *Daemon) addressKnownLocked(dev, sess string) bool {
	if dev == d.device {
		return sess == "" || sess == proto.CLISession || d.sessions[sess] != nil
	}
	p := d.peers[dev]
	if p == nil || d.staleLocked(p) {
		return false
	}
	// Every daemon delivers to its own CLI, which presence doesn't list.
	if sess == "" || sess == proto.CLISession {
		return true
	}
	for _, s := range p.pres.Sessions {
		if s.ID == sess {
			return true
		}
	}
	return false
}

// sessionInfoLocked describes the session at addr, from this device or a peer's presence.
func (d *Daemon) sessionInfoLocked(addr string) (name, agent, branch string) {
	dev, sess := bus.SplitAddr(addr)
	if dev == d.device {
		if s := d.sessions[sess]; s != nil {
			return s.name, s.agent, s.branch
		}
		return "", "", ""
	}
	if p := d.peers[dev]; p != nil {
		for _, s := range p.pres.Sessions {
			if s.ID == sess {
				return s.Name, s.Agent, s.Branch
			}
		}
	}
	return "", "", ""
}

func (d *Daemon) messageViewsLocked(msgs []*inMsg, withBody bool) []proto.MessageView {
	views := make([]proto.MessageView, 0, len(msgs))
	var byAddr map[string]string // the registry's names, for a sender no longer in presence
	for _, m := range msgs {
		e := m.Event
		name, agent, branch := d.sessionInfoLocked(e.From)
		if name == "" {
			if byAddr == nil {
				byAddr = d.namesByAddrLocked()
			}
			name = byAddr[e.From]
		}
		v := proto.MessageView{ID: e.ID, From: e.From, FromName: name, FromAgent: agent, FromBranch: branch,
			To: e.To, Subject: e.Subject, At: e.At}
		if withBody {
			v.Body = e.Body
		}
		views = append(views, v)
	}
	return views
}

// whoLocked lists every device and session. The CLI shows only when it is the one asking.
func (d *Daemon) whoLocked(you string) []proto.Device {
	self := proto.Device{ID: d.device, Host: d.host, Owner: d.owner, Self: true, LastSeen: time.Now()}
	for _, s := range d.sortedSessionsLocked() {
		self.Sessions = append(self.Sessions, proto.SessionView{Addr: bus.Addr(d.device, s.id), Name: s.name,
			Pending: s.name == "", Agent: s.agent, Branch: s.branch, Doing: s.doing, Started: s.started,
			You: s.id == you})
	}
	if you == proto.CLISession {
		self.Sessions = append(self.Sessions, proto.SessionView{Addr: bus.Addr(d.device, proto.CLISession),
			Agent: "cli", Started: d.started, You: true})
	}
	devices := []proto.Device{self}
	for _, id := range sortedKeys(d.peers) {
		p := d.peers[id]
		dv := proto.Device{ID: id, Host: p.pres.Host, Owner: p.pres.Owner, LastSeen: p.lastChange,
			Stale: d.staleLocked(p)}
		for _, s := range p.pres.Sessions {
			if s.ID == proto.CLISession {
				continue // V1 daemons list their CLI, which only claims needed
			}
			dv.Sessions = append(dv.Sessions, proto.SessionView{Addr: bus.Addr(id, s.ID), Name: s.Name,
				Agent: s.Agent, Branch: s.Branch, Doing: s.Doing, Started: s.Started})
		}
		devices = append(devices, dv)
	}
	return devices
}

// newSessionLocked registers a session that isn't registered. One that stopped within
// StoppedInbox gets its inbox back, with what arrived while it was stopped, and keeps its claim on
// what was sent since it stopped: a catch-up that reads a backlog ref only after the session has
// registered again would otherwise take those messages for older than the session and drop them.
// Past StoppedInbox, pruneInboxes is only late. Caller holds mu.
func (d *Daemon) newSessionLocked(h *proto.Hello, agent, branch, key string) *session {
	// A key that has a name keeps it, whatever this session's ID, and whichever process
	// registered it: the name is waiting here from the first moment, without the registry.
	s := &session{id: h.ID, agent: agent, cwd: h.Cwd, claudeSession: h.ClaudeSession, branch: branch,
		started: time.Now(), reconnect: h.Reconnect, key: key, clientPID: h.ClientPID}
	d.setNameLocked(s, d.st.NameKeys[key])
	if at, ok := d.st.Stopped[h.ID]; ok {
		delete(d.st.Stopped, h.ID)
		if time.Since(at) > d.opt.StoppedInbox {
			delete(d.st.Inbox, h.ID)
		} else {
			s.since = at
		}
		d.saveLocked()
	}
	d.sessions[h.ID] = s
	return s
}
