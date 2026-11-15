package daemon_test

import (
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/daemon"
	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/view"
)

// A message handed to a T3 thread as a turn is read once the turn starts (#611): the hook's
// UserPromptSubmit carries the prompt, and a prompt that holds the message marks it. One the user
// typed meanwhile doesn't.
func TestATurnsMessageIsReadWhenItsPromptComes(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	exe, calls := useFakeT3(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	bob, _ := b.sessionRoute("bbbbbb", "claude-bob", proto.Route{T3CLI: exe, T3Thread: "thread-bob"})
	bobName := b.named(bob)
	pa := a.page()
	pa.choose("ada")
	eventually(t, "presence", func() bool { return a.seesSession(bob) })

	id := pa.sendText(bobName, "Can you rerun CI?\nIt flaked on Windows.")
	eventually(t, "the handoff", func() bool { return len(calls()) > 0 })
	eventually(t, "handed over", pa.stateIs(id, bob, bus.MarkHanded))
	turn := calls()[0].Stdin

	// The user's own prompt, typed while T3 held Murmur's turn: still handed over.
	b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-bob", Whole: true, Prompt: "fix the lint errors"})
	b.do(proto.Request{Op: proto.OpTurnEnd, ClaudeSession: "claude-bob"})
	if r, _ := pa.receipt(id, bob); r.State != bus.MarkHanded {
		t.Fatalf("after the user's own prompt the receipt is %+v, want still handed over", r)
	}

	// Murmur's turn starts: its prompt is what was handed over.
	b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-bob", Whole: true, Prompt: turn})
	eventually(t, "read: the turn holding it started", pa.stateIs(id, bob, bus.MarkRead))
}

// A turn's prompt marks its own message, not an older one whose subject starts the same (#651): a
// turn for "Deploy" was cancelled, and the prompt of the turn for "Deploy now" has "Deploy" at the
// start of a line.
func TestATurnMarksOnlyItsOwnMessage(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	exe, calls := useFakeT3(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	bob, _ := b.sessionRoute("bbbbbb", "claude-bob", proto.Route{T3CLI: exe, T3Thread: "thread-bob"})
	bobName := b.named(bob)
	pa := a.page()
	pa.choose("ada")
	eventually(t, "presence", func() bool { return a.seesSession(bob) })

	cancelled := pa.sendText(bobName, "Deploy")
	eventually(t, "the first handoff", func() bool { return len(calls()) == 1 })
	eventually(t, "the first handed over", pa.stateIs(cancelled, bob, bus.MarkHanded))
	b.do(proto.Request{Op: proto.OpTurnEnd, ClaudeSession: "claude-bob"}) // the turn never started

	id := pa.sendText(bobName, "Deploy now")
	eventually(t, "the second handoff", func() bool { return len(calls()) == 2 })
	eventually(t, "the second handed over", pa.stateIs(id, bob, bus.MarkHanded))

	b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-bob", Whole: true, Prompt: calls()[1].Stdin})
	eventually(t, "read: the turn for it started", pa.stateIs(id, bob, bus.MarkRead))
	if r, _ := pa.receipt(cancelled, bob); r.State != bus.MarkHanded {
		t.Fatalf("the cancelled turn's message is %+v, want still handed over", r)
	}
}

// A prompt is matched to the turn view.Turn rendered, as the daemon handed it over: after a line a
// client put before it, cut at the hook's 4 KB, or with its line ends changed. A prompt the user
// typed, or another message's turn, isn't it.
func TestAPromptIsMatchedToItsTurn(t *testing.T) {
	msg := func(subject, body string) proto.MessageView {
		return proto.MessageView{From: "d/aaaaaa", FromName: "quiet-otter", To: "d/bbbbbb", Subject: subject, Body: body}
	}
	rerun := view.Turn(msg("Rerun CI?", "It flaked."))
	cut := view.Turn(msg("A long first line that the page cut…", "A long first line that the page cut short, and went on."))
	long := view.Turn(msg("Logs", strings.Repeat("é line of the log\n", 400)))
	for _, c := range []struct{ name, prompt, turn string }{
		{"the turn itself", rerun, rerun},
		{"a subject that starts the body", cut, cut},
		{"after a line a client put first", "[from T3]\n" + rerun, rerun},
		{"with Windows line ends, trimmed", strings.TrimSpace(strings.ReplaceAll(rerun, "\n", "\r\n")), rerun},
		{"cut at the hook's 4 KB", long[:proto.MaxPrompt-1], long},
	} {
		if !daemon.IsTurn(c.prompt, c.turn) {
			t.Errorf("%s: the prompt isn't taken for its turn:\n%s", c.name, c.prompt)
		}
	}

	deploy := view.Turn(msg("Deploy", ""))
	for _, c := range []struct{ name, prompt, turn string }{
		{"a longer subject that starts the same", view.Turn(msg("Deploy now", "")), deploy},
		{"a shorter one", deploy, view.Turn(msg("Deploy now", ""))},
		{"the same subject, another body", view.Turn(msg("Rerun CI?", "It flaked again.")), rerun},
		{"the user's own words", "run the tests", view.Turn(msg("test", ""))},
		{"the user's words under a line like a header", "Message me later\ntest", view.Turn(msg("test", ""))},
		{"the header alone", strings.SplitN(rerun, "\n", 2)[0], rerun},
	} {
		if daemon.IsTurn(c.prompt, c.turn) {
			t.Errorf("%s: the prompt is taken for another turn:\n%s", c.name, c.prompt)
		}
	}
}
