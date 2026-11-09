package daemon_test

import (
	"context"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/proto"
)

// Pi sessions get #202's delivery through Murmur's extension (#205): it runs the hook, which names
// the session by Pi's own ID for it (the session's file, Native), and opens a socket that takes
// Claude Code's messaging-socket lines.

// piHook is the hook as Pi's extension runs it, for the session whose file is file: after a prompt
// or a tool call (OpNotices), or at the end of its work (OpTurnEnd, which can keep it going).
func (d *device) piHook(op, file string) proto.Response {
	d.t.Helper()
	r := d.do(proto.Request{Op: op, Native: file, Whole: true, Continues: op == proto.OpTurnEnd})
	if r.Error != "" {
		d.t.Fatalf("%s: %s", op, r.Error)
	}
	return r
}

// While a Pi session works its message is a notice, at the end of its work the message whole keeps
// it going, and once it is idle a message goes in through the extension's socket, as for Claude Code.
func TestPiSessionGetsNoticesWhileBusyAndTurnsWhenIdle(t *testing.T) {
	remote := newRemote(t)
	sock := useFakeSocket(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	alice, _ := a.session("aaaaaa", "")
	const file = "/home/u/.pi/agent/sessions/--work--/2026-11-09_pi.jsonl"
	conn, resp, err := b.c.Hello(context.Background(), proto.Hello{ID: "bbbbbb", Agent: "pi",
		Route: proto.Route{Native: file, MsgSocket: sock.path, MsgToken: "tok-pi"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	bob := resp.Self
	eventually(t, "presence", func() bool { return a.seesSession(bob) && b.seesSession(alice) })

	// One turn, so the daemon knows its end is seen.
	b.piHook(proto.OpNotices, file)
	b.piHook(proto.OpTurnEnd, file)

	b.piHook(proto.OpNotices, file) // a prompt: at work
	a.send("aaaaaa", bob, "while busy", "the body")
	var r proto.Response
	eventually(t, "the notice", func() bool { r = b.piHook(proto.OpNotices, file); return len(r.Messages) > 0 })
	if r.Messages[0].Subject != "while busy" || r.Messages[0].Body != "" || r.Unread != 1 {
		t.Errorf("the notice: %+v, unread %d", r.Messages, r.Unread)
	}
	holds(t, "nothing on the socket while it works", time.Second, func() bool { return len(sock.lines()) == 0 })

	if got := b.piHook(proto.OpTurnEnd, file).Delivered; len(got) != 1 || got[0].Body != "the body" {
		t.Fatalf("the end of its work gave %+v", got)
	}
	if got := b.piHook(proto.OpTurnEnd, file).Delivered; len(got) != 0 {
		t.Fatalf("the next end gave %v", viewSubjects(got))
	}

	a.send("aaaaaa", bob, "while idle", "wake up")
	eventually(t, "the turn on the socket", func() bool { return len(sock.lines()) >= 2 })
	socketTurn(t, sock.lines(), "tok-pi")
}

// The hook of a Pi session speaks only for the session with its file: not another agent's session,
// which has no Claude Code ID either, nor a Claude Code session.
func TestPiHookSpeaksOnlyForItsSession(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	a.session("aaaaaa", "")
	other, _ := b.sessionRoute("cccccc", "", proto.Route{Native: "codex-thread-1"})
	claude, _ := b.sessionRoute("dddddd", "claude-d", proto.Route{Native: "/some/file.jsonl"})
	eventually(t, "presence", func() bool { return a.seesSession(other) && a.seesSession(claude) })

	a.send("aaaaaa", other, "for the other", "")
	a.send("aaaaaa", claude, "for claude", "")
	eventually(t, "both to reach b", func() bool { return logHas(b, "for the other") && logHas(b, "for claude") })
	for _, file := range []string{"/some/file.jsonl", "/no/such/file.jsonl"} {
		if r := b.piHook(proto.OpNotices, file); len(r.Messages) != 0 || r.Self != "" {
			t.Errorf("a Pi hook for %s got %+v", file, r)
		}
	}
	if inbox, _ := b.unread("cccccc", ""); len(inbox) != 1 {
		t.Errorf("the other session's inbox: %v", inbox)
	}
}
