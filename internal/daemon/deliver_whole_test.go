package daemon_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/view"
)

// Tests of the page's choice of what a session at work gets from the hook (#204): the subject
// only, or the whole message as the turn it would have been.

// busySetup is a device b with a hooked session bob in T3, which a message from alice's device a
// could be handed to `t3` for, and the stand-in's calls.
type busySetup struct {
	a, b               *device
	alice, bob         string // the sessions' addresses
	aliceName, bobName string
	calls              func() []t3Call
	putSettings        func(string)                                       // PUT /api/settings on b, which must be accepted
	hook               func(extra ...func(*proto.Request)) proto.Response // bob's hook, as after a tool call
	arrived            func(subject string)                               // waits for the message to reach b
}

func newBusySetup(t *testing.T) *busySetup {
	t.Helper()
	remote := newRemote(t)
	exe, calls := useFakeT3(t)
	s := &busySetup{calls: calls}
	s.a, s.b = newDevice(t, remote), newDevice(t, remote)
	s.alice, _ = s.a.session("aaaaaa", "")
	s.bob, _ = s.b.sessionRoute("bbbbbb", "claude-bob", proto.Route{T3CLI: exe, T3Thread: "thread-bob"})
	s.aliceName, s.bobName = s.a.named(s.alice), s.b.named(s.bob)
	eventually(t, "presence", func() bool { return s.a.seesSession(s.bob) && s.b.seesSession(s.alice) })
	pg := s.b.page()
	s.putSettings = func(settings string) {
		t.Helper()
		if code, body := pg.call("PUT", "/api/settings", pg.token, settings); code != http.StatusOK {
			t.Fatalf("PUT %s: %d %s", settings, code, body)
		}
	}
	s.hook = func(extra ...func(*proto.Request)) proto.Response {
		req := proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-bob"}
		for _, f := range extra {
			f(&req)
		}
		return s.b.do(req)
	}
	s.arrived = func(subject string) {
		t.Helper()
		eventually(t, "the message to reach b", func() bool { return logHas(s.b, subject) })
	}
	return s
}

func (s *busySetup) inbox() []proto.MessageView {
	return s.b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages
}

// By default a session at work is told the subject, and the body waits for `inbox`.
func TestSubjectOnlyIsTheDefaultForAWorkingSession(t *testing.T) {
	s := newBusySetup(t)
	s.hook() // a prompt or a tool call: the session is at work
	s.a.send("aaaaaa", s.bob, "plan?", "the whole plan")
	s.arrived("plan?")

	r := s.hook()
	if len(r.Whole) != 0 || len(r.Messages) != 1 || r.Messages[0].Subject != "plan?" || r.Messages[0].Body != "" || r.Unread != 1 {
		t.Fatalf("notices: %+v", r)
	}
	if in := s.inbox(); len(in) != 1 || in[0].Body != "the whole plan" {
		t.Errorf("the message should wait in the inbox: %+v", in)
	}
	holds(t, "nothing handed to T3 while at work", time.Second, func() bool { return len(s.calls()) == 0 })
}

// With the device set to the whole message, a session at work gets it through the hook: the same
// text as the turn, in the reply to the hook, and it is out of the inbox, said so in the log, and
// not counted. Nothing is handed to T3, then or when the turn ends.
func TestWholeMessageIsDeliveredThroughTheHook(t *testing.T) {
	s := newBusySetup(t)
	s.putSettings(`{"wake":{"when":"within"},"busy":"whole"}`)
	s.hook()
	body := "Are you changing it?\nI say \"yes\" — and 100% über-sure.\n\n  indented"
	s.a.send("aaaaaa", s.bob, "auth middleware?", body)
	s.arrived("auth middleware?")

	r := s.hook()
	if len(r.Whole) != 1 || len(r.Messages) != 0 || r.Unread != 0 {
		t.Fatalf("notices: %+v", r)
	}
	turn := view.Turn(r.Whole[0])
	for _, want := range []string{"Message from " + s.aliceName + " (test", "auth middleware?\n\n" + body + "\n", "Reply with"} {
		if !strings.Contains(turn, want) {
			t.Errorf("the turn lacks %q:\n%s", want, turn)
		}
	}
	if in := s.inbox(); len(in) != 0 {
		t.Errorf("a delivered message is still in the inbox: %+v", in)
	}
	if !logHas(s.b, `delivered "auth middleware?"`) || !logHas(s.b, "through the hook") {
		t.Errorf("the log doesn't say it was delivered through the hook: %+v", s.b.do(proto.Request{Op: proto.OpLog, Limit: 50}).Events)
	}
	if again := s.hook(); len(again.Whole)+len(again.Messages) != 0 || again.Unread != 0 {
		t.Errorf("the message came a second time: %+v", again)
	}

	// The turn ends: nothing is left to hand to T3.
	s.b.do(proto.Request{Op: proto.OpTurnEnd, ClaudeSession: "claude-bob"})
	holds(t, "no turn for a message the hook delivered", 1500*time.Millisecond, func() bool { return len(s.calls()) == 0 })

	// It was saved: a restart doesn't bring it back.
	s.b.restart()
	if in := s.inbox(); len(in) != 0 {
		t.Errorf("after a restart the inbox holds %+v", in)
	}
}

// Whole messages go in order, and a message that was only announced before the choice changed
// stays unread and is counted; the ones delivered whole are not.
func TestWholeMessagesDoNotCountAsUnread(t *testing.T) {
	s := newBusySetup(t)
	s.hook()
	s.a.send("aaaaaa", s.bob, "before", "told by subject")
	s.arrived("before")
	if r := s.hook(); len(r.Messages) != 1 || r.Unread != 1 {
		t.Fatalf("the first notice: %+v", r)
	}

	s.putSettings(`{"wake":{"when":"within"},"busy":"whole"}`)
	s.a.send("aaaaaa", s.bob, "one", "first body")
	s.a.send("aaaaaa", s.bob, "two", "second body")
	s.arrived("two")
	r := s.hook()
	if len(r.Whole) != 2 || r.Whole[0].Subject != "one" || r.Whole[1].Subject != "two" || r.Whole[1].Body != "second body" {
		t.Fatalf("whole messages: %+v", r.Whole)
	}
	if len(r.Messages) != 0 {
		t.Errorf("announced as well: %+v", r.Messages)
	}
	// The one told by subject is still unread, and its count has not changed, so it isn't repeated.
	if r.Unread != 0 {
		t.Errorf("the count was said again: %d", r.Unread)
	}
	if in := s.inbox(); len(in) != 1 || in[0].Subject != "before" {
		t.Errorf("the inbox holds %+v, want the one told by subject", in)
	}
}

// A session's own choice beats the device's, both ways; the choice is by the session's name.
func TestSessionChoiceOverridesTheDeviceOnesForWhatAWorkingSessionGets(t *testing.T) {
	s := newBusySetup(t)
	s.hook()

	s.putSettings(`{"wake":{"when":"within"},"busy":"whole","sessionBusy":{"` + s.bobName + `":"subject"}}`)
	s.a.send("aaaaaa", s.bob, "device whole, session subject", "x")
	s.arrived("device whole, session subject")
	if r := s.hook(); len(r.Whole) != 0 || len(r.Messages) != 1 {
		t.Errorf("the session's subject choice: %+v", r)
	}

	s.putSettings(`{"wake":{"when":"within"},"busy":"subject","sessionBusy":{"` + s.bobName + `":"whole"}}`)
	s.a.send("aaaaaa", s.bob, "device subject, session whole", "y")
	s.arrived("device subject, session whole")
	if r := s.hook(); len(r.Whole) != 1 || len(r.Messages) != 0 || r.Whole[0].Body != "y" {
		t.Errorf("the session's whole choice: %+v", r)
	}

	// Another session's choice is not this one's.
	s.putSettings(`{"wake":{"when":"within"},"busy":"subject","sessionBusy":{"someone-else":"whole"}}`)
	s.a.send("aaaaaa", s.bob, "someone else's choice", "z")
	s.arrived("someone else's choice")
	if r := s.hook(); len(r.Whole) != 0 || len(r.Messages) != 1 {
		t.Errorf("another session's choice reached this one: %+v", r)
	}
}

// The choice is for a session at work. One that is idle gets a turn, as it did, whatever the
// choice, and the wake setting still rules that.
func TestAnIdleSessionStillGetsATurnWhateverTheChoice(t *testing.T) {
	s := newBusySetup(t)
	s.putSettings(`{"wake":{"when":"always"},"busy":"whole"}`)
	s.hook()
	s.b.do(proto.Request{Op: proto.OpTurnEnd, ClaudeSession: "claude-bob"}) // idle
	s.a.send("aaaaaa", s.bob, "wake up", "now")
	eventually(t, "the turn", func() bool { return len(s.calls()) == 1 })
	if c := s.calls()[0]; !strings.Contains(c.Stdin, "wake up") || !strings.Contains(c.Stdin, "now") {
		t.Errorf("the turn: %q", c.Stdin)
	}
	eventually(t, "the message to be marked read", func() bool { return logHas(s.b, "as a turn") })
}

// A subagent's tool call is told nothing, as before: the notice would land in the subagent. The
// message stays in the inbox, whatever the choice.
func TestWholeMessageIsNotGivenToASubagent(t *testing.T) {
	s := newBusySetup(t)
	s.putSettings(`{"wake":{"when":"within"},"busy":"whole"}`)
	s.hook()
	s.a.send("aaaaaa", s.bob, "for the parent", "body")
	s.arrived("for the parent")

	if r := s.hook(func(r *proto.Request) { r.Subagent = true }); len(r.Whole)+len(r.Messages) != 0 {
		t.Fatalf("a subagent's hook got %+v", r)
	}
	// (Reading the inbox marks what it shows read, so the parent's hook comes first.)
	if r := s.hook(); len(r.Whole) != 1 || r.Whole[0].Body != "body" {
		t.Errorf("the parent's next hook: %+v", r)
	}
}

// Whole messages are for a session whose hook runs. A call to OpNotices that is not the hook's
// (no Claude Code session ID) is not at work as far as the daemon knows, and gets the notice.
func TestWholeMessageNeedsTheHook(t *testing.T) {
	s := newBusySetup(t)
	s.putSettings(`{"wake":{"when":"never"},"busy":"whole"}`)
	s.a.send("aaaaaa", s.bob, "no hook", "body")
	s.arrived("no hook")
	r := s.b.do(proto.Request{Op: proto.OpNotices, Session: "bbbbbb"})
	if len(r.Whole) != 0 || len(r.Messages) != 1 {
		t.Errorf("without the hook: %+v", r)
	}
	if in := s.inbox(); len(in) != 1 {
		t.Errorf("the inbox holds %+v", in)
	}
}
