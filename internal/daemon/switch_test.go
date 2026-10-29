package daemon_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/t4/internal/hook"
	"github.com/a-Fig/t4/internal/proto"
)

// Tests of a session following Claude Code's session ID (#9) and of a name found at the session
// that holds it (#37). Claude Code changes a conversation's session ID on /clear, /branch and a
// /resume of another; the MCP server keeps the one it started with, and only the hook sees the
// live one. The hook's CLAUDE_PID is its Claude Code process, which is the MCP server's parent.

// sessionPID is session for an MCP server whose parent process is pid. It returns the whole reply.
func (d *device) sessionPID(id, claude string, pid int) (string, net.Conn, proto.Response) {
	d.t.Helper()
	conn, resp, err := d.c.Hello(context.Background(),
		proto.Hello{ID: id, Agent: "test", ClaudeSession: claude, ClientPID: pid})
	if err != nil {
		d.t.Fatalf("hello %s: %v", id, err)
	}
	d.t.Cleanup(func() { conn.Close() })
	return resp.Self, conn, resp
}

// helloPID is a server of process pid registering the session id, again: it retries while the
// daemon hasn't yet seen the previous connection close.
func (d *device) helloPID(id, claude string, pid int) (net.Conn, proto.Response) {
	d.t.Helper()
	var conn net.Conn
	var resp proto.Response
	eventually(d.t, "a session to register again", func() bool {
		c, r, err := d.c.Hello(context.Background(),
			proto.Hello{ID: id, Agent: "test", ClaudeSession: claude, ClientPID: pid, Reconnect: true})
		if err != nil {
			return false
		}
		d.t.Cleanup(func() { c.Close() })
		conn, resp = c, r
		return true
	})
	return conn, resp
}

// hook is what the Claude Code hook asks the daemon: the notices of conversation claude, running
// in process pid.
func (d *device) hook(claude string, pid int) []string {
	d.t.Helper()
	var subjects []string
	for _, m := range d.do(proto.Request{Op: proto.OpNotices, ClaudeSession: claude, ClientPID: pid}).Messages {
		subjects = append(subjects, m.Subject)
	}
	return subjects
}

// noticed waits for the hook of (claude, pid) to be told of the message with this subject.
func (d *device) noticed(claude string, pid int, subject string) {
	d.t.Helper()
	eventually(d.t, "a notice of "+subject, func() bool {
		for _, s := range d.hook(claude, pid) {
			if s == subject {
				return true
			}
		}
		return false
	})
}

// nameHeld waits for d's who to show the session at addr under a name other than the ones given,
// and returns it.
func (d *device) nameHeld(addr string, not ...string) string {
	d.t.Helper()
	var name string
	eventually(d.t, "a new name for "+addr, func() bool {
		name = d.nameOf(addr)
		if name == "" {
			return false
		}
		for _, n := range not {
			if name == n {
				return false
			}
		}
		return true
	})
	return name
}

// nameIs waits for d's who to show the session at addr under exactly this name.
func (d *device) nameIs(addr, want string) {
	d.t.Helper()
	eventually(d.t, addr+" to be called "+want, func() bool { return d.nameOf(addr) == want })
}

const (
	procP = 4242 // the Claude Code process of the sessions under test
	procQ = 4343 // another one
)

// A /clear in a session: the MCP server still says conversation A, the hook says B. The session
// follows: it shows another name to everyone, notices for B reach it, and a /resume back to A
// brings A's name back, with nothing but presence telling the others.
func TestSwitchFollowsTheHooksClaudeID(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	sender, _ := a.session("aaaaaa", "claude-sender")
	bob, _, _ := b.sessionPID("bbbbbb", "claude-a", procP)
	nameA := b.named(bob)
	eventually(t, "presence", func() bool { return a.nameOf(bob) == nameA && b.seesSession(sender) })

	a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: nameA, Subject: "for A"})
	b.noticed("claude-a", procP, "for A")

	// /clear: the hook is the first to hear of the new conversation.
	b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-b", ClientPID: procP})
	nameB := b.nameHeld(bob, nameA)
	a.nameIs(bob, nameB)

	a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: nameB, Subject: "for B"})
	b.noticed("claude-b", procP, "for B")
	// What the notices only announced is in the inbox, under the session's one inbox.
	if got := inboxOf(b, "bbbbbb"); len(got) != 2 {
		t.Fatalf("inbox after the switch: %v, want both messages", got)
	}

	// /resume back to A.
	b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-a", ClientPID: procP})
	b.nameIs(bob, nameA)
	a.nameIs(bob, nameA)
	if !logHas(b, "followed Claude Code") {
		t.Fatalf("the switch is not in the log: %+v", b.do(proto.Request{Op: proto.OpLog, Limit: 50}).Events)
	}
	// Nothing started or stopped: it is the same session.
	if n := busLines(b, bob, "started"); n != 1 {
		t.Fatalf("the switch wrote %d starts for the session", n)
	}
	if n := busLines(b, bob, "stopped"); n != 0 {
		t.Fatalf("the switch wrote a stop for the session")
	}
	noNameTwice(t, readRegistry(t, remote))
}

// A conversation Claude Code starts and throws away (the one it opens with before a /resume) gets
// a name that is never used again; the session then follows to the conversation that matters, which
// is a new one (a new name, as #20 assigns) or one named in an earlier run, whose name it gets back.
func TestSwitchAfterAThrowawayStart(t *testing.T) {
	remote := newRemote(t)
	b := newTimedDevice(t, remote, 300*time.Millisecond, 0)

	// An earlier run of conversation X: named, then stopped.
	old, oldConn, _ := b.sessionPID("cccccc", "claude-x", procP)
	nameX := b.named(old)
	oldConn.Close()
	eventually(t, "the earlier run to stop", func() bool { return busLines(b, old, "stopped") == 1 })

	// A new process opens on a throwaway conversation T.
	bob, _, _ := b.sessionPID("dddddd", "claude-throwaway", procQ)
	nameT := b.named(bob)
	if nameT == nameX {
		t.Fatalf("a new conversation took the name %s of another", nameX)
	}

	// The user resumes X: the hook reports X for process Q.
	b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-x", ClientPID: procQ})
	b.nameIs(bob, nameX)

	// And a conversation never seen before gets a name of its own.
	b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-y", ClientPID: procQ})
	b.nameHeld(bob, nameX, nameT)
	noNameTwice(t, readRegistry(t, remote))
}

// A hook's ID and PID that match no session change nothing, and a PID never moves a session that
// belongs to another process.
func TestSwitchNeverTakesAnotherSession(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	sender, _ := a.session("aaaaaa", "claude-sender")
	p, _, _ := b.sessionPID("bbbbbb", "claude-p", procP)
	q, _, _ := b.sessionPID("cccccc", "claude-q", procQ)
	nameP, nameQ := b.named(p), b.named(q)
	eventually(t, "presence", func() bool { return a.nameOf(p) == nameP && a.nameOf(q) == nameQ && b.seesSession(sender) })

	a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: nameP, Subject: "for P"})
	a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: nameQ, Subject: "for Q"})
	eventually(t, "delivery", func() bool { return logHas(b, "for P") && logHas(b, "for Q") })

	// A hook with an ID and a process nobody has: nothing changes and nothing is told.
	if got := b.hook("claude-stranger", 999); len(got) != 0 {
		t.Fatalf("a stranger's hook got %v", got)
	}
	// A hook of process Q with a conversation of its own: it moves Q's session, and P's notices
	// are not Q's.
	if got := b.hook("claude-q2", procQ); len(got) != 1 || got[0] != "for Q" {
		t.Fatalf("Q's hook got %v", got)
	}
	nameQ2 := b.nameHeld(q, nameQ)
	if got := b.nameOf(p); got != nameP {
		t.Fatalf("a hook of process Q renamed P's session from %s to %s", nameP, got)
	}
	// A hook of process Q that names P's conversation: the ID matches P's session, but the process
	// is Q's, so P's notices are not told to it and P keeps its name.
	if got := b.hook("claude-p", procQ); len(got) != 0 {
		t.Fatalf("Q's hook, with P's conversation ID, got %v", got)
	}
	// P's session still answers to its own conversation and process.
	if got := b.hook("claude-p", procP); len(got) != 1 || got[0] != "for P" {
		t.Fatalf("P's hook got %v", got)
	}
	// The stranger, once more: no one's name moved.
	b.hook("claude-stranger", 999)
	holds(t, "P's name", time.Second, func() bool { return b.nameOf(p) == nameP })
	b.nameHeld(q, nameQ, nameQ2, nameP) // Q's session has moved on twice, to names of its own
	if n := busLines(b, p, "stopped") + busLines(b, q, "stopped"); n != 0 {
		t.Fatalf("a session stopped")
	}
}

// The MCP server of a session that switched registers again, as a restarted one does, with the ID
// its environment still has. The session keeps the new ID and its name, which the reply says so the
// server can use it from then on; a server of another process has no such claim and is taken as it says.
func TestReconnectAfterASwitchKeepsTheLiveID(t *testing.T) {
	remote := newRemote(t)
	b := newTimedDevice(t, remote, 10*time.Second, 0)
	bob, conn, _ := b.sessionPID("bbbbbb", "claude-a", procP)
	nameA := b.named(bob)

	b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-b", ClientPID: procP})
	nameB := b.nameHeld(bob, nameA)

	// Any call tells the server the live ID.
	if got := b.do(proto.Request{Op: proto.OpWho, Session: "bbbbbb"}).ClaudeSession; got != "claude-b" {
		t.Fatalf("the daemon says the live ID is %q", got)
	}

	// A restarted server, still with A in its environment: the same process, so the daemon's ID stands.
	conn.Close()
	conn, resp := b.helloPID("bbbbbb", "claude-a", procP)
	if resp.ClaudeSession != "claude-b" || resp.Name != nameB {
		t.Fatalf("a restarted server got ID %q and name %q, want claude-b and %s", resp.ClaudeSession, resp.Name, nameB)
	}
	holds(t, "the new name", time.Second, func() bool { return b.nameOf(bob) == nameB })

	// The server, now knowing B, says so: the same.
	conn.Close()
	conn, resp = b.helloPID("bbbbbb", "claude-b", procP)
	if resp.ClaudeSession != "claude-b" || resp.Name != nameB {
		t.Fatalf("a server that knew got ID %q and name %q, want claude-b and %s", resp.ClaudeSession, resp.Name, nameB)
	}

	// A server of another process is trusted: that is a conversation on its own.
	conn.Close()
	_, resp = b.helloPID("bbbbbb", "claude-a", procQ)
	if resp.ClaudeSession != "claude-a" || resp.Name != nameA {
		t.Fatalf("a server of another process got ID %q and name %q, want claude-a and %s", resp.ClaudeSession, resp.Name, nameA)
	}
}

// Two MCP servers in one Claude Code process, which is what a project and a user config for t4
// make: both move to the conversation the hook reports, and since a name is an address the two do
// not share one.
func TestTwoServersInOneProcessSwitchTogether(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	first, _, _ := b.sessionPID("bbbbbb", "claude-a", procP)
	second, _, _ := b.sessionPID("cccccc", "claude-a", procP)
	n1, n2 := b.named(first), b.named(second)
	if n1 == n2 {
		t.Fatalf("two live sessions of one conversation both called %s", n1)
	}
	eventually(t, "presence", func() bool { return a.nameOf(first) == n1 && a.nameOf(second) == n2 })

	b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-b", ClientPID: procP})
	m1, m2 := b.nameHeld(first, n1, n2), b.nameHeld(second, n1, n2)
	if m1 == m2 {
		t.Fatalf("both servers of conversation B are called %s", m1)
	}
	eventually(t, "presence", func() bool { return a.nameOf(first) == m1 && a.nameOf(second) == m2 })

	// And back: the names each had before.
	b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-a", ClientPID: procP})
	b.nameIs(first, n1)
	b.nameIs(second, n2)
	noNameTwice(t, readRegistry(t, remote))
}

// A message for a name a session had before it moved to another conversation reaches the session,
// which is the same session with the same inbox, and the sender is told it is called something
// else now. A send to the new name has no such note.
func TestSendToAnOldNameReachesTheSession(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	sender, _ := a.session("aaaaaa", "claude-sender")
	bob, _, _ := b.sessionPID("bbbbbb", "claude-a", procP)
	nameA := b.named(bob)
	eventually(t, "presence", func() bool { return a.nameOf(bob) == nameA && b.seesSession(sender) })

	b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-b", ClientPID: procP})
	nameB := b.nameHeld(bob, nameA)
	a.nameIs(bob, nameB)

	// From another device, which reads the old name from the registry.
	r := a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: nameA, Subject: "to the old name"})
	if r.Error != "" || !strings.Contains(r.Note, nameA) || !strings.Contains(r.Note, nameB) {
		t.Fatalf("send to the old name: %+v", r)
	}
	b.noticed("claude-b", procP, "to the old name")

	// From the same device.
	r = b.do(proto.Request{Op: proto.OpSend, To: nameA, Subject: "to the old name, here"})
	if r.Error != "" || !strings.Contains(r.Note, nameB) {
		t.Fatalf("send to the old name on its own device: %+v", r)
	}
	b.noticed("claude-b", procP, "to the old name, here")

	r = a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: nameB, Subject: "to the new name"})
	if r.Error != "" || r.Note != "" {
		t.Fatalf("send to the current name: %+v", r)
	}
}

// #37: a /resume back to an earlier conversation puts its name on a session whose ID is not the one
// the registry has the name down for. When that session stops too, a send by name must not go to
// the first session's inbox, which is dead; it goes to the one that holds the name now.
func TestNameOfAResumedConversationFindsTheSessionThatHoldsIt(t *testing.T) {
	remote := newRemote(t)
	a := newDevice(t, remote)
	b := newTimedDevice(t, remote, 300*time.Millisecond, 0)
	a.session("aaaaaa", "claude-sender")

	// Conversation X runs as session one and stops.
	first, conn1, _ := b.sessionPID("cccccc", "claude-x", procP)
	nameX := b.named(first)
	a.nameIs(first, nameX)
	conn1.Close()
	eventually(t, "the first to stop", func() bool { return busLines(b, first, "stopped") == 1 })

	// X, resumed, is a new session under a new ID, and gets the name back.
	second, conn2, _ := b.sessionPID("dddddd", "claude-x", procQ)
	b.nameIs(second, nameX)
	conn2.Close()
	eventually(t, "the second to stop", func() bool { return busLines(b, second, "stopped") == 1 })
	// The sender no longer sees either in presence, so it has only the registry's word for the name.
	eventually(t, "presence to empty", func() bool { return !a.seesSession(first) && !a.seesSession(second) })

	r := a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: nameX, Subject: "to a resumed conversation"})
	if r.Error != "" {
		t.Fatalf("send: %+v", r)
	}
	eventually(t, "delivery", func() bool { return logHas(b, "to a resumed conversation") })

	// Both come back, to read what they were sent.
	b.sessionPID("dddddd", "claude-x", procQ)
	if got := inboxOf(b, "dddddd"); len(got) != 1 || got[0] != "to a resumed conversation" {
		t.Fatalf("the session that holds %s got %v", nameX, got)
	}
	b.sessionPID("cccccc", "claude-z", procP)
	if got := b.do(proto.Request{Op: proto.OpInbox, Session: "cccccc"}).Messages; len(got) != 0 {
		t.Fatalf("the retired session got %+v", got)
	}
}

// #37 with a switch: a session that followed Claude Code to an earlier conversation holds that
// conversation's name, and a message for the session the registry has it down for (its address, or
// its name once the holder has gone) is the holder's.
func TestMessageForARetiredIDGoesToTheHolder(t *testing.T) {
	remote := newRemote(t)
	a := newDevice(t, remote)
	b := newTimedDevice(t, remote, 300*time.Millisecond, 0)
	a.session("aaaaaa", "claude-sender")

	first, conn1, _ := b.sessionPID("cccccc", "claude-x", procP)
	nameX := b.named(first)
	conn1.Close()
	eventually(t, "the first to stop", func() bool { return busLines(b, first, "stopped") == 1 })

	// A session that began on something else and was moved to X by a /resume.
	second, _, _ := b.sessionPID("dddddd", "claude-other", procQ)
	b.named(second)
	b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-x", ClientPID: procQ})
	b.nameIs(second, nameX)

	// The first session's address is a retired one: what is sent to it is the holder's.
	a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: first, Subject: "to the retired address"})
	b.noticed("claude-x", procQ, "to the retired address")

	// The retired session, back under another conversation, has nothing waiting.
	b.sessionPID("cccccc", "claude-z", procP)
	if got := b.do(proto.Request{Op: proto.OpInbox, Session: "cccccc"}).Messages; len(got) != 0 {
		t.Fatalf("the retired session got %+v", got)
	}
}

// The real hook: a payload on stdin and CLAUDE_PID in its environment drive a switch end to end, and
// the notice for the new conversation comes out on stdout.
func TestHookRunDrivesASwitch(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	sender, _ := a.session("aaaaaa", "claude-sender")
	bob, _, _ := b.sessionPID("bbbbbb", "claude-a", procP)
	nameA := b.named(bob)
	eventually(t, "presence", func() bool { return a.nameOf(bob) == nameA && b.seesSession(sender) })

	run := func(claude string) string {
		t.Helper()
		payload, _ := json.Marshal(map[string]string{"session_id": claude, "hook_event_name": "UserPromptSubmit",
			"cwd": filepath.Dir(b.gitDir)})
		t.Setenv("CLAUDE_PID", strconv.Itoa(procP))
		var out bytes.Buffer
		hook.Run(bytes.NewReader(payload), &out)
		return out.String()
	}

	if out := run("claude-b"); out != "" {
		t.Fatalf("the hook printed %q with nothing to tell", out)
	}
	nameB := b.nameHeld(bob, nameA)
	a.nameIs(bob, nameB)

	a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: nameB, Subject: "for the new conversation"})
	var out string
	eventually(t, "the hook to print the notice", func() bool {
		out = run("claude-b")
		return out != ""
	})
	if !strings.Contains(out, "for the new conversation") {
		t.Fatalf("the hook printed %q", out)
	}
}
