package daemon_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/proto"
)

// At the end of a busy turn, the Stop hook keeps the turn going with what the session hasn't read,
// rather than the daemon handing it over as a new turn (#202, Tyler, 2026-11-08).

// stopHook is Claude Code's Stop hook at the end of a turn of Claude session claude: it can keep the
// turn going, and the reply's Delivered is what it gives the agent.
func (d *device) stopHook(claude string) proto.Response {
	d.t.Helper()
	r := d.do(proto.Request{Op: proto.OpTurnEnd, ClaudeSession: claude, Continues: true})
	if r.Error != "" {
		d.t.Fatalf("turnend: %s", r.Error)
	}
	return r
}

// What the session was told of and what came untold during the turn are given whole at its end,
// and none goes to T3. The turn goes on, so a message that comes then waits for its next end, not
// T3's queue; once a Stop gives nothing, the session is idle and a message wakes it as before.
func TestTheStopHookKeepsTheTurnGoing(t *testing.T) {
	remote := newRemote(t)
	exe, calls := useFakeT3(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.sessionRoute("bbbbbb", "claude-bob", proto.Route{T3CLI: exe, T3Thread: "thread-bob"})
	eventually(t, "presence", func() bool { return a.seesSession(bob) && b.seesSession(alice) })

	b.hookNotices("claude-bob")
	if r := b.stopHook("claude-bob"); len(r.Delivered) != 0 {
		t.Fatalf("a Stop with nothing for it gave %v", viewSubjects(r.Delivered))
	}
	b.hookNotices("claude-bob") // a prompt: the session is at work
	a.send("aaaaaa", bob, "told", "the body of the told one")
	eventually(t, "the notice", func() bool { return len(b.hookNotices("claude-bob").Messages) > 0 })
	a.send("aaaaaa", bob, "untold", "")
	eventually(t, "the message to reach b", func() bool { return logHas(b, "untold") })

	r := b.stopHook("claude-bob")
	if got := viewSubjects(r.Delivered); len(got) != 2 || got[0] != "told" || got[1] != "untold" {
		t.Fatalf("the Stop gave %v; want [told untold]", got)
	}
	if r.Delivered[0].Body != "the body of the told one" {
		t.Errorf("the body given: %q", r.Delivered[0].Body)
	}
	if inbox, notices := b.unread("bbbbbb", "claude-bob"); len(inbox) != 0 || len(notices) != 0 {
		t.Errorf("after the Stop gave them: inbox %v, notices %v", inbox, notices)
	}

	a.send("aaaaaa", bob, "in the continued turn", "")
	eventually(t, "the next message to reach b", func() bool { return logHas(b, "in the continued turn") })
	holds(t, "nothing handed to T3 while the turn goes on", time.Second, func() bool { return len(calls()) == 0 })
	if got := viewSubjects(b.stopHook("claude-bob").Delivered); len(got) != 1 || got[0] != "in the continued turn" {
		t.Fatalf("the next Stop gave %v", got)
	}

	if r := b.stopHook("claude-bob"); len(r.Delivered) != 0 {
		t.Fatalf("a Stop after the last message gave %v", viewSubjects(r.Delivered))
	}
	a.send("aaaaaa", bob, "while idle", "")
	eventually(t, "the idle session woken through T3", func() bool { return len(calls()) == 1 })
}

// Outside T3 with no messaging socket, the Stop hook is the one way in: what came during the turn
// is given at its end, where before it waited in the inbox.
func TestTheStopHookIsAWayInWithoutASocket(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.session("bbbbbb", "claude-bob")
	eventually(t, "presence", func() bool { return a.seesSession(bob) && b.seesSession(alice) })

	b.hookNotices("claude-bob")
	b.stopHook("claude-bob")
	b.hookNotices("claude-bob")
	a.send("aaaaaa", bob, "only way in", "")
	eventually(t, "the notice", func() bool { return len(b.hookNotices("claude-bob").Messages) > 0 })
	if got := viewSubjects(b.stopHook("claude-bob").Delivered); len(got) != 1 || got[0] != "only way in" {
		t.Fatalf("the Stop gave %v", got)
	}
}

// A message the agent read with inbox during the turn isn't given again at its end, and the page's
// never-wake setting keeps the rest in the inbox, as it keeps them from being turns.
func TestTheStopHookGivesWhatATurnWould(t *testing.T) {
	remote := newRemote(t)
	exe, _ := useFakeT3(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.sessionRoute("bbbbbb", "claude-bob", proto.Route{T3CLI: exe, T3Thread: "thread-bob"})
	eventually(t, "presence", func() bool { return a.seesSession(bob) && b.seesSession(alice) })

	b.hookNotices("claude-bob")
	b.stopHook("claude-bob")
	b.hookNotices("claude-bob")
	a.send("aaaaaa", bob, "read mid-turn", "")
	eventually(t, "the notice", func() bool { return len(b.hookNotices("claude-bob").Messages) > 0 })
	b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"})
	if got := viewSubjects(b.stopHook("claude-bob").Delivered); len(got) != 0 {
		t.Fatalf("the Stop gave %v, read already", got)
	}

	pg := b.page()
	if code, body := pg.call("PUT", "/api/settings", pg.token, `{"wake":{"when":"never"}}`); code != http.StatusOK {
		t.Fatalf("PUT settings: %d %s", code, body)
	}
	b.hookNotices("claude-bob")
	a.send("aaaaaa", bob, "never wake", "")
	eventually(t, "the notice", func() bool { return len(b.hookNotices("claude-bob").Messages) > 0 })
	if got := viewSubjects(b.stopHook("claude-bob").Delivered); len(got) != 0 {
		t.Fatalf("the Stop gave %v under never", got)
	}
	if inbox, _ := b.unread("bbbbbb", "claude-bob"); len(inbox) != 1 {
		t.Errorf("inbox under never: %v; want the message kept", inbox)
	}
}

// A message the session was told of before its first Stop, when there was no known way in, is
// given at that Stop: the Stop is the way in.
func TestTheFirstStopGivesWhatWasToldBeforeIt(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.session("bbbbbb", "claude-bob")
	eventually(t, "presence", func() bool { return a.seesSession(bob) && b.seesSession(alice) })

	b.hookNotices("claude-bob")
	a.send("aaaaaa", bob, "told in the first turn", "")
	eventually(t, "the notice", func() bool { return len(b.hookNotices("claude-bob").Messages) > 0 })
	if got := viewSubjects(b.stopHook("claude-bob").Delivered); len(got) != 1 || got[0] != "told in the first turn" {
		t.Fatalf("the first Stop gave %v", got)
	}
}

// Claude Code ends a turn after eight continuations in a row, whatever the hook says: the ninth
// Stop with no tool call between gives nothing, and its message is handed over as a turn instead.
// A tool call starts the count again.
func TestContinuationsStopAtClaudeCodesCap(t *testing.T) {
	remote := newRemote(t)
	exe, calls := useFakeT3(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.sessionRoute("bbbbbb", "claude-bob", proto.Route{T3CLI: exe, T3Thread: "thread-bob"})
	eventually(t, "presence", func() bool { return a.seesSession(bob) && b.seesSession(alice) })

	b.hookNotices("claude-bob")
	b.stopHook("claude-bob")
	b.hookNotices("claude-bob")
	for i := 0; i < 9; i++ {
		subject := fmt.Sprintf("message %d", i+1)
		a.send("aaaaaa", bob, subject, "")
		eventually(t, subject+" to reach b", func() bool { return logHas(b, subject) })
		got := viewSubjects(b.stopHook("claude-bob").Delivered)
		switch {
		case i < 8 && (len(got) != 1 || got[0] != subject):
			t.Fatalf("Stop %d gave %v", i+1, got)
		case i == 8 && len(got) != 0:
			t.Fatalf("the ninth Stop in a row gave %v", got)
		}
	}
	eventually(t, "the ninth handed over as a turn", func() bool { return len(calls()) == 1 })

	b.hookNotices("claude-bob") // a new turn
	a.send("aaaaaa", bob, "after a tool call", "")
	eventually(t, "the message to reach b", func() bool { return logHas(b, "after a tool call") })
	if got := viewSubjects(b.stopHook("claude-bob").Delivered); len(got) != 1 {
		t.Fatalf("a Stop after the count started again gave %v", got)
	}
}
