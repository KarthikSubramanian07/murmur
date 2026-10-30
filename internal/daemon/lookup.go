package daemon

import (
	"fmt"
	"strings"

	"github.com/a-Fig/t4/internal/bus"
	"github.com/a-Fig/t4/internal/names"
	"github.com/a-Fig/t4/internal/proto"
)

// lookup (#21) gathers what the daemon knows about one session: what presence shows of anyone's,
// and, for a session on this device, what never leaves it. The messages are the ones this daemon
// has read, which is what the page keeps too: the last PageMessages, none from before it started.

const (
	defaultLookupMessages = 10
	maxLookupMessages     = 50
	// lookupCommits is how many of a local session's credited commits lookup lists, newest last.
	lookupCommits = 20
)

// lookup answers OpLookup for the session you, who asked.
func (d *Daemon) lookup(you string, req proto.Request) proto.Response {
	limit := req.Limit
	if limit <= 0 {
		limit = defaultLookupMessages
	}
	limit = min(limit, maxLookupMessages)
	d.settleEnds()
	d.mu.Lock()
	defer d.mu.Unlock()
	addr, err := d.lookupAddrLocked(strings.TrimSpace(req.Name))
	if err != nil {
		return fail(err.Error())
	}
	return proto.Response{OK: true, Self: bus.Addr(d.device, you), Lookup: d.detailLocked(addr, you, limit)}
}

// lookupAddrLocked turns what the caller named into the address of a session this daemon knows:
// running here or on another device, stopped here, or only in the registry. An unknown one is an
// error. Caller holds mu.
func (d *Daemon) lookupAddrLocked(target string) (string, error) {
	switch {
	case target == "":
		return "", fmt.Errorf("lookup needs a session's name (who shows them) or its device/session address")
	case strings.Contains(target, "/"):
		dev, sess := bus.SplitAddr(target)
		if !bus.ValidDevice(dev) || !bus.ValidName(sess) || sess == proto.CLISession {
			return "", fmt.Errorf("%q isn't a session's address; lookup takes a name or device/session", target)
		}
		if !d.sessionKnownLocked(dev, sess) {
			return "", fmt.Errorf("no session is known at %q. who shows the names and addresses in use.", target)
		}
		return target, nil
	case names.Valid(target):
		addr, ok := d.resolveNameLocked(target)
		if !ok {
			return "", fmt.Errorf("no session is named %q. who shows the names in use.", target)
		}
		return addr, nil
	}
	return "", fmt.Errorf("%q isn't a session's name or address; lookup takes a name like quiet-otter, or device/session", target)
}

// sessionKnownLocked says whether the daemon has anything on the session sess of device dev: it
// is running there, it stopped here, or the registry has its address. Caller holds mu.
func (d *Daemon) sessionKnownLocked(dev, sess string) bool {
	addr := bus.Addr(dev, sess)
	if dev == d.device {
		_, stopped := d.st.Stopped[sess]
		if d.sessions[sess] != nil || stopped {
			return true
		}
	} else if p := d.peers[dev]; p != nil {
		for _, s := range p.pres.Sessions {
			if s.ID == sess {
				return true
			}
		}
	}
	for _, a := range d.st.Used {
		if a == addr {
			return true
		}
	}
	return false
}

// detailLocked describes the session at addr. you is the session asking. Caller holds mu.
func (d *Daemon) detailLocked(addr, you string, limit int) *proto.SessionDetail {
	dev, sess := bus.SplitAddr(addr)
	byAddr := d.namesByAddrLocked()
	det := &proto.SessionDetail{Addr: addr, Device: dev, You: dev == d.device && sess == you}
	if dev == d.device {
		det.Local, det.Host, det.Owner = true, d.host, d.owner
		det.Committed = d.committedLocked(sess)
		if s := d.sessions[sess]; s != nil {
			det.Name, det.Agent, det.Branch, det.Doing, det.Started = s.name, s.agent, s.branch, s.doing, s.started
			det.Running, det.Reachable, det.Dropped = true, s.dropped.IsZero(), !s.dropped.IsZero()
			det.LastActive, det.Worktree, det.ClientSession = s.lastActive, s.worktree, s.claudeSession
		} else {
			det.Name = d.st.SessionNames[sess]
			det.StoppedAt = d.st.Stopped[sess]
		}
		recs := d.st.Commits[sess]
		det.CommitCount = len(recs)
		for _, r := range recs[max(len(recs)-lookupCommits, 0):] {
			det.Commits = append(det.Commits, proto.CommitView{SHA: r.SHA, Branch: r.Branch, Subject: r.Subject, At: r.At})
		}
	} else if p := d.peers[dev]; p != nil {
		det.Host, det.Owner = p.pres.Host, p.pres.Owner
		det.DeviceSeen, det.Stale = p.lastChange, d.staleLocked(p)
		for _, s := range p.pres.Sessions {
			if s.ID != sess {
				continue
			}
			det.Name, det.Agent, det.Branch, det.Doing, det.Started = s.Name, s.Agent, s.Branch, s.Doing, s.Started
			det.Committed, det.LastActive = s.Committed, peerActive(p, s)
			det.Running, det.Reachable = true, !det.Stale
		}
	}
	if det.Name == "" {
		det.Name = byAddr[addr]
	}
	det.Messages = d.messagesOfLocked(addr, det.Name, byAddr, limit)
	return det
}

// messagesOfLocked lists the last limit messages to or from the session at addr, oldest first. A
// conversation that came back under a new session ID keeps its name, so the other addresses with
// that name count as the same session, as the page's filter does. Caller holds mu.
func (d *Daemon) messagesOfLocked(addr, name string, byAddr map[string]string, limit int) []proto.MessageView {
	label := func(a string) string {
		if n, _, _ := d.sessionInfoLocked(a); n != "" {
			return n
		}
		return byAddr[a]
	}
	is := func(a string) bool { return a == addr || name != "" && label(a) == name }
	var out []proto.MessageView
	for i := len(d.msgs) - 1; i >= 0 && len(out) < limit; i-- {
		e := d.msgs[i].e
		toSession := strings.Contains(e.To, "/")
		if !is(e.From) && !(toSession && is(e.To)) {
			continue
		}
		v := proto.MessageView{ID: e.ID, From: e.From, FromName: label(e.From), To: e.To, Subject: e.Subject, At: e.At}
		if toSession {
			v.ToName = label(e.To)
		}
		out = append(out, v)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
