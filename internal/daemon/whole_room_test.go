package daemon_test

import (
	"testing"

	"github.com/a-Fig/murmur/internal/daemon"
)

// The hook's reply speaks for every session of its agent's process, and the messages it gives whole
// leave their inboxes before it goes. A reply longer than a client reads (#371) would lose them, so
// a Stop gives only what fits, and the rest waits in the inbox for the next one. Serial: it replaces
// a package variable.
func TestTheStopHookGivesOnlyWhatFitsAReply(t *testing.T) {
	t.Cleanup(daemon.SetMaxWholeReply(1500)) // one short message's room, and not two
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.session("bbbbbb", "claude-bob")
	eventually(t, "presence", func() bool { return a.seesSession(bob) && b.seesSession(alice) })

	b.hookNotices("claude-bob")
	b.stopHook("claude-bob")
	b.hookNotices("claude-bob") // a prompt: the session is at work
	a.send("aaaaaa", bob, "first", "its body")
	eventually(t, "the first to reach b", func() bool { return logHas(b, "first") })
	a.send("aaaaaa", bob, "second", "its body")
	eventually(t, "the second to reach b", func() bool { return logHas(b, "second") })

	if got := viewSubjects(b.stopHook("claude-bob").Read); len(got) != 1 || got[0] != "first" {
		t.Fatalf("the Stop gave %v; want [first], all that fits", got)
	}
	if got := viewSubjects(b.stopHook("claude-bob").Read); len(got) != 1 || got[0] != "second" {
		t.Fatalf("the next Stop gave %v; want [second], kept for it", got)
	}
}
