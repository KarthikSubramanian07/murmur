package daemon_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/view"
)

// The page chooses what the hook gives a session at work, for the device or one session (#204).
// A whole message is the turn's exact text and is delivered through the hook: it leaves the inbox,
// isn't counted unread, isn't handed to T3 when the turn ends, and is noted as delivered.
func TestBusyMessageSetting(t *testing.T) {
	remote := newRemote(t)
	exe, calls := useFakeT3(t)
	var out syncBuf
	a, b := newDevice(t, remote), newDeviceWith(t, remote, func(d *device) { d.logTo = &out })
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.sessionRoute("bbbbbb", "claude-bob", proto.Route{T3CLI: exe, T3Thread: "thread-bob"})
	bobName := b.named(bob)
	eventually(t, "presence", func() bool { return a.seesSession(bob) && b.seesSession(alice) })
	b.hook("claude-bob", 0) // the hook has seen the session at work, so nothing goes to T3

	pg := b.page()
	put := func(settings string) {
		t.Helper()
		if code, body := pg.call("PUT", "/api/settings", pg.token, settings); code != http.StatusOK {
			t.Fatalf("PUT %s: %d %s", settings, code, body)
		}
	}
	next := func(subject string) proto.Response {
		t.Helper()
		var resp proto.Response
		eventually(t, "the hook's notice of "+subject, func() bool {
			resp = b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-bob"})
			return len(resp.Messages) == 1 && resp.Messages[0].Subject == subject
		})
		return resp
	}
	readInbox := func(want int) {
		t.Helper()
		resp := b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"})
		if len(resp.Messages) != want {
			t.Fatalf("inbox has %d messages, want %d: %+v", len(resp.Messages), want, resp.Messages)
		}
	}

	// A request from before the setting existed leaves the default in force.
	put(`{"wake":{"when":"always"}}`)
	a.send("aaaaaa", bob, "subject by default", "read this only if needed")
	resp := next("subject by default")
	if msg := resp.Messages[0]; msg.Whole || msg.Body != "" || resp.Unread != 1 {
		t.Errorf("default notice: %+v, unread %d", msg, resp.Unread)
	}
	readInbox(1)

	put(`{"wake":{"when":"always"},"busy":"whole"}`)
	a.send("aaaaaa", bob, "whole for the device", "the whole body")
	resp = next("whole for the device")
	msg := resp.Messages[0]
	if !msg.Whole || msg.Body != "the whole body" || resp.Unread != 0 {
		t.Errorf("whole notice: %+v, unread %d", msg, resp.Unread)
	}
	if got, want := view.Notices(resp.Messages, resp.Unread), view.Turn(msg); got != want {
		t.Errorf("hook text:\n got %q\nwant %q", got, want)
	}
	readInbox(0)

	// The session's subject choice overrides the device's whole-message choice.
	put(`{"wake":{"when":"always"},"busy":"whole","sessionBusy":{"` + bobName + `":"subject"}}`)
	a.send("aaaaaa", bob, "subject for the session", "still in the inbox")
	resp = next("subject for the session")
	if msg := resp.Messages[0]; msg.Whole || msg.Body != "" || resp.Unread != 1 {
		t.Errorf("session's subject notice: %+v, unread %d", msg, resp.Unread)
	}
	readInbox(1)

	// And the other way around.
	put(`{"wake":{"when":"always"},"busy":"subject","sessionBusy":{"` + bobName + `":"whole"}}`)
	a.send("aaaaaa", bob, "whole for the session", "delivered now")
	resp = next("whole for the session")
	if msg := resp.Messages[0]; !msg.Whole || msg.Body != "delivered now" || resp.Unread != 0 {
		t.Errorf("session's whole notice: %+v, unread %d", msg, resp.Unread)
	}
	readInbox(0)

	noted := false
	for _, event := range b.do(proto.Request{Op: proto.OpLog, Limit: 100}).Events {
		if strings.Contains(event.Line, `delivered "whole for the device"`) && strings.Contains(event.Line, "through the hook") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("the page's notes don't say the hook delivered it: %+v", b.do(proto.Request{Op: proto.OpLog, Limit: 100}).Events)
	}
	if !strings.Contains(out.String(), "delivered message") || !strings.Contains(out.String(), "through the hook") {
		t.Errorf("the daemon's log doesn't say the hook delivered it:\n%s", out.String())
	}

	b.do(proto.Request{Op: proto.OpTurnEnd, ClaudeSession: "claude-bob"})
	holds(t, "nothing handed to T3 after the turn", 800*time.Millisecond, func() bool { return len(calls()) == 0 })
}
