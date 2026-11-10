package daemon_test

import (
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/testgit"
)

// GitHub now and then refuses a push as busy, "[remote rejected] (failed)", when it meets another
// push to the same repository. A message whose push is refused that way is sent again, so the send
// works and the message arrives once, written to the log once.
func TestSendOutlastsABusyRefusal(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.session("bbbbbb", "claude-bob")
	eventually(t, "each device to see the other's session", func() bool {
		return a.seesSession(bob) && b.seesSession(alice)
	})

	refused := testgit.RefusePushes(t, remote, brand.MeshPrefix+a.self(), "failed", 1, false)
	if r := a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: bob, Subject: "auth middleware?"}); r.Error != "" {
		t.Fatalf("send: %s", r.Error)
	}
	if n := refused(); n != 1 {
		t.Fatalf("the remote refused %d pushes of the log, want 1", n)
	}
	if got := inboxWant(b, "bbbbbb", 1); len(got) != 1 {
		t.Fatalf("bob's inbox: %q, want the message once", got)
	}
	// Each commit's subject line, "<from> -> <to>: <subject>", is in its message once.
	if n := strings.Count(busText(t, remote, a), " -> "+bob+": auth middleware?\n"); n != 1 {
		t.Fatalf("the message is on the log %d times", n)
	}
}
