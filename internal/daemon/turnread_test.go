package daemon_test

import (
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

// The turn view.Turn renders holds the subject the daemon matches it by, whether it prints the
// subject or, for a subject that only starts the body, the body.
func TestATurnHoldsItsSubject(t *testing.T) {
	for _, m := range []proto.MessageView{
		{From: "d/aaaaaa", FromName: "quiet-otter", To: "d/bbbbbb", Subject: "Rerun CI?", Body: "It flaked."},
		{From: "d/aaaaaa", FromName: "quiet-otter", To: "d/bbbbbb", Subject: "A long first line that the page cut…", Body: "A long first line that the page cut short, and went on."},
	} {
		if !daemon.Holds(view.Turn(m), m.Subject) {
			t.Errorf("the turn for %q isn't matched by its subject:\n%s", m.Subject, view.Turn(m))
		}
	}
}
