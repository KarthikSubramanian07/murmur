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
	"github.com/a-Fig/t4/internal/proto"
	"github.com/a-Fig/t4/internal/scrub"
)

// maxBody caps a message body. Everything on the bus stays in history for good.
const maxBody = 32 << 10

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
	writeResp(conn, d.handle(ctx, req))
}

func writeResp(conn net.Conn, resp proto.Response) {
	resp.OK = resp.OK || resp.Error == ""
	b, _ := json.Marshal(resp)
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	conn.Write(append(b, '\n'))
}

// hello registers a session and holds its connection open until the session's process exits.
// The connection closing is the session stopping, however the process ended.
func (d *Daemon) hello(ctx context.Context, conn net.Conn, r *bufio.Reader, h *proto.Hello) {
	if h == nil || !validName(h.ID) || h.ID == proto.CLISession {
		writeResp(conn, fail("hello needs a session ID of lowercase letters, digits and dashes"))
		return
	}
	agent := bus.OneLine(h.Agent, 40)
	if agent == "" {
		agent = "unknown"
	}
	s := &session{id: h.ID, agent: agent, cwd: h.Cwd, claudeSession: h.ClaudeSession,
		branch: branchOf(ctx, h.Cwd), started: time.Now(), reconnect: h.Reconnect}
	d.mu.Lock()
	if d.sessions[h.ID] != nil {
		d.mu.Unlock()
		writeResp(conn, fail("session ID "+h.ID+" is in use"))
		return
	}
	d.sessions[h.ID] = s
	d.lastActive = time.Now()
	if h.Reconnect {
		d.catchUpLocked(h.ID)
	}
	d.mu.Unlock()

	addr := bus.Addr(d.device, h.ID)
	d.log.Printf("session %s started (%s, %s)", addr, agent, s.branch)
	writeResp(conn, proto.Response{OK: true, Self: addr})
	d.kickPresence()
	d.appendSoon(bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.SessionStarted, From: addr,
		At: time.Now().UTC(), Agent: agent, Branch: s.branch})

	stopWatch := context.AfterFunc(ctx, func() { conn.Close() })
	io.Copy(io.Discard, r) // returns when the session's process closes the connection, or dies
	stopWatch()

	d.mu.Lock()
	delete(d.sessions, h.ID)
	d.lastActive = time.Now()
	if ctx.Err() != nil {
		// The daemon is stopping. The session may reconnect to the next one, so its inbox is
		// kept; the goodbye ends this device's presence, and its claims with it.
		d.mu.Unlock()
		return
	}
	delete(d.st.Inbox, h.ID)
	d.saveLocked()
	d.mu.Unlock()
	d.log.Printf("session %s stopped", addr)
	d.kickPresence()
	// Still inside this connection's handler, so the daemon waits for these before exiting.
	d.releaseHeld(addr)
	d.appendSoon(bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.SessionStopped, From: addr,
		At: time.Now().UTC()})
}

func (d *Daemon) handle(ctx context.Context, req proto.Request) proto.Response {
	sess := req.Session
	if sess == "" {
		sess = proto.CLISession
	}
	d.mu.Lock()
	d.lastActive = time.Now()
	registered := sess == proto.CLISession || d.sessions[sess] != nil
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
		return proto.Response{OK: true, Self: from, Devices: d.whoLocked(sess)}

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

	case proto.OpClaims:
		var note string
		if err := d.pollOnce(ctx); err != nil {
			note = "Couldn't reach the remote; these are the claims as last synced: " + err.Error()
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		return proto.Response{OK: true, Self: from, Claims: claimViews(d.openLocked(-1, "")), Seen: d.claimsTip, Note: note}

	case proto.OpClaim:
		return d.claim(ctx, from, req)

	case proto.OpRelease:
		return d.release(ctx, from, strings.TrimSpace(req.Claim))

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

// notices returns the session's messages it hasn't been told about yet, without bodies, and
// marks them told. The hook calls it; the hook knows only Claude Code's session ID.
func (d *Daemon) notices(req proto.Request) proto.Response {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := req.Session
	if req.ClaudeSession != "" {
		id = ""
		for _, s := range d.sessions {
			if s.claudeSession == req.ClaudeSession {
				id = s.id
				break
			}
		}
	}
	if id == "" {
		return proto.Response{OK: true}
	}
	var fresh []*inMsg
	for _, m := range d.st.Inbox[id] {
		if !m.Noticed {
			m.Noticed = true
			fresh = append(fresh, m)
		}
	}
	if len(fresh) > 0 {
		d.saveLocked()
	}
	return proto.Response{OK: true, Self: bus.Addr(d.device, id), Messages: d.messageViewsLocked(fresh, false)}
}

func (d *Daemon) send(ctx context.Context, from string, req proto.Request) proto.Response {
	to := strings.TrimSpace(req.To)
	subject := bus.OneLine(req.Subject, 120)
	if to == "" || subject == "" {
		return fail("a message needs a recipient (device/session, a device, or * for everyone) and a subject")
	}
	if len(req.Body) > maxBody {
		return fail(fmt.Sprintf("not sent: the body is over %d KB, and the bus keeps everything forever", maxBody>>10))
	}
	if err := scrub.Check(subject, req.Body); err != nil {
		return fail(err.Error())
	}
	var note string
	if to != "*" {
		dev, sess := bus.SplitAddr(to)
		d.mu.Lock()
		known := d.addressKnownLocked(dev, sess)
		d.mu.Unlock()
		if !known {
			note = to + " isn't in any device's presence right now, so nobody may read this. Check `who` for addresses."
		}
	}
	e := bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message, From: from, At: time.Now().UTC(),
		To: to, Subject: subject, Body: req.Body}
	if err := d.appendEvent(ctx, e); err != nil {
		return fail("not sent: " + err.Error())
	}
	return proto.Response{OK: true, Self: from, Note: note,
		Messages: []proto.MessageView{{ID: e.ID, From: from, To: to, Subject: subject, At: e.At}}}
}

func (d *Daemon) addressKnownLocked(dev, sess string) bool {
	if dev == d.device {
		return sess == "" || sess == proto.CLISession || d.sessions[sess] != nil
	}
	p := d.peers[dev]
	if p == nil || d.staleLocked(p) {
		return false
	}
	if sess == "" {
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
func (d *Daemon) sessionInfoLocked(addr string) (agent, branch string) {
	dev, sess := bus.SplitAddr(addr)
	if dev == d.device {
		if s := d.sessions[sess]; s != nil {
			return s.agent, s.branch
		}
		return "", ""
	}
	if p := d.peers[dev]; p != nil {
		for _, s := range p.pres.Sessions {
			if s.ID == sess {
				return s.Agent, s.Branch
			}
		}
	}
	return "", ""
}

func (d *Daemon) messageViewsLocked(msgs []*inMsg, withBody bool) []proto.MessageView {
	views := make([]proto.MessageView, 0, len(msgs))
	for _, m := range msgs {
		e := m.Event
		agent, branch := d.sessionInfoLocked(e.From)
		v := proto.MessageView{ID: e.ID, From: e.From, FromAgent: agent, FromBranch: branch, To: e.To,
			Subject: e.Subject, At: e.At}
		if withBody {
			v.Body = e.Body
		}
		views = append(views, v)
	}
	return views
}

// whoLocked lists every device and session. The CLI shows only when it holds claims, or when
// it is the one asking.
func (d *Daemon) whoLocked(you string) []proto.Device {
	now := time.Now()
	claimsBy := func(addr string) []proto.ClaimView {
		var out []proto.ClaimView
		for _, c := range d.claims {
			if c.ev.From == addr && d.claimLiveLocked(c, now) {
				out = append(out, claimView(c.ev))
			}
		}
		return out
	}
	self := proto.Device{ID: d.device, Host: d.host, Owner: d.owner, Self: true, LastSeen: now}
	for _, s := range d.sortedSessionsLocked() {
		addr := bus.Addr(d.device, s.id)
		self.Sessions = append(self.Sessions, proto.SessionView{Addr: addr, Agent: s.agent, Branch: s.branch,
			Doing: s.doing, Started: s.started, You: s.id == you, Claims: claimsBy(addr)})
	}
	cli := bus.Addr(d.device, proto.CLISession)
	if held := claimsBy(cli); len(held) > 0 || you == proto.CLISession {
		self.Sessions = append(self.Sessions, proto.SessionView{Addr: cli, Agent: "cli", Started: d.started,
			You: you == proto.CLISession, Claims: held})
	}
	devices := []proto.Device{self}
	for _, id := range sortedKeys(d.peers) {
		p := d.peers[id]
		dv := proto.Device{ID: id, Host: p.pres.Host, Owner: p.pres.Owner, LastSeen: p.lastChange,
			Stale: d.staleLocked(p)}
		for _, s := range p.pres.Sessions {
			addr := bus.Addr(id, s.ID)
			held := claimsBy(addr)
			if s.ID == proto.CLISession && len(held) == 0 {
				continue
			}
			dv.Sessions = append(dv.Sessions, proto.SessionView{Addr: addr, Agent: s.Agent, Branch: s.Branch,
				Doing: s.Doing, Started: s.Started, Claims: held})
		}
		devices = append(devices, dv)
	}
	return devices
}
