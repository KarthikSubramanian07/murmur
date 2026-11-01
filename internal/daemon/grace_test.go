package daemon_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/proto"
)

// Tests of a session whose MCP server process restarts: its connection drops and the same
// session ID registers again, while the conversation carries on.

// busLines counts the lines in d's log that session addr started or stopped; verb is "started"
// or "stopped".
func busLines(d *device, addr, verb string) int {
	n := 0
	for _, ev := range d.do(proto.Request{Op: proto.OpLog, Limit: 500}).Events {
		if strings.HasPrefix(ev.Line, addr+" "+verb) {
			n++
		}
	}
	return n
}

// meshTip is the tip of dev's log on the remote.
func meshTip(t *testing.T, d *device, remote, dev string) string {
	t.Helper()
	return git(t, filepath.Dir(d.gitDir), "ls-remote", remote, "refs/murmur/mesh/"+dev)
}

// restartedMCP is a session's MCP server process, new, registering under the session's ID again.
func (d *device) restartedMCP(id, claude string) (net.Conn, error) {
	conn, _, err := d.c.Hello(context.Background(), proto.Hello{ID: id, Agent: "test", ClaudeSession: claude})
	if err == nil {
		d.t.Cleanup(func() { conn.Close() })
	}
	return conn, err
}

// logHas says whether d's log shows an event whose line contains text.
func logHas(d *device, text string) bool {
	for _, ev := range d.do(proto.Request{Op: proto.OpLog, Limit: 500}).Events {
		if strings.Contains(ev.Line, text) {
			return true
		}
	}
	return false
}

// A connection that drops and comes back inside the grace is the same session: it never leaves
// presence, nothing is written to the bus, and it keeps its inbox and its unread notices. The test
// runs past the end of the first grace, so a stop written when that grace ends would show.
func TestReconnectInGraceKeepsSession(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newTimedDevice(t, remote, 2*time.Second, 0)
	bob, conn := b.session("bbbbbb", "claude-bob")
	eventually(t, "presence", func() bool { return a.seesSession(bob) })
	eventually(t, "bob's start on the bus", func() bool { return busLines(b, bob, "started") == 1 })

	a.do(proto.Request{Op: proto.OpSend, To: bob, Subject: "told about"})
	eventually(t, "a notice", func() bool {
		return len(b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-bob"}).Messages) == 1
	})
	a.do(proto.Request{Op: proto.OpSend, To: bob, Subject: "not yet told about"})
	eventually(t, "delivery", func() bool { return logHas(b, "not yet told about") })
	tip := meshTip(t, b, remote, b.self())

	conn.Close()
	holds(t, "bob in presence while his connection is down", time.Second, func() bool {
		return a.seesSession(bob) && b.seesSession(bob)
	})
	if _, err := b.restartedMCP("bbbbbb", "claude-bob"); err != nil {
		t.Fatalf("the same session registering again: %v", err)
	}
	holds(t, "bob in presence past the first grace", 3*time.Second, func() bool {
		return a.seesSession(bob) && b.seesSession(bob)
	})

	if got := meshTip(t, b, remote, b.self()); got != tip {
		t.Fatalf("something was written to the bus: %s -> %s", tip, got)
	}
	if n := busLines(b, bob, "started") + busLines(b, bob, "stopped"); n != 1 {
		t.Fatalf("the log has %d start or stop lines for bob, want his one start", n)
	}
	notices := b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-bob"}).Messages
	if len(notices) != 1 || notices[0].Subject != "not yet told about" {
		t.Fatalf("notices after the restart: %+v", notices)
	}
	if got := b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages; len(got) != 2 {
		t.Fatalf("inbox after the restart: %+v", got)
	}
}

// When the grace runs out, the session stops as it did before: it leaves presence, and
// session.stopped is written, but not before.
func TestGraceRunsOut(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newTimedDevice(t, remote, 2*time.Second, 0)
	bob, conn := b.session("bbbbbb", "")
	eventually(t, "presence", func() bool { return a.seesSession(bob) })
	eventually(t, "bob's start on the bus", func() bool { return busLines(b, bob, "started") == 1 })

	conn.Close()
	dropped := time.Now()
	holds(t, "bob in presence and not stopped, inside the grace", time.Second, func() bool {
		return a.seesSession(bob) && busLines(b, bob, "stopped") == 0
	})
	eventually(t, "bob stopped", func() bool { return busLines(b, bob, "stopped") == 1 })
	if time.Since(dropped) < 2*time.Second {
		t.Fatalf("stopped %s after the drop, inside the grace", time.Since(dropped))
	}
	eventually(t, "bob gone from a", func() bool { return !a.seesSession(bob) })
	if b.seesSession(bob) {
		t.Fatal("bob is still in b's presence")
	}
}

// A message sent while the session's connection is down is held, and reaches it after it
// reconnects: its notice waits until then, and comes once.
func TestMessageInGraceIsDeliveredAfterReconnect(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newTimedDevice(t, remote, 10*time.Second, 0)
	bob, conn := b.session("bbbbbb", "claude-bob")
	eventually(t, "presence", func() bool { return a.seesSession(bob) })

	conn.Close()
	if r := a.do(proto.Request{Op: proto.OpSend, To: bob, Subject: "while you were gone"}); r.Error != "" || r.Note != "" {
		t.Fatalf("send: %+v", r)
	}
	eventually(t, "delivery", func() bool { return logHas(b, "while you were gone") })
	if n := b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-bob"}).Messages; len(n) != 0 {
		t.Fatalf("a notice for a session that is not connected: %+v", n)
	}

	if _, err := b.restartedMCP("bbbbbb", "claude-bob"); err != nil {
		t.Fatal(err)
	}
	notices := b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-bob"}).Messages
	if len(notices) != 1 || notices[0].Subject != "while you were gone" {
		t.Fatalf("notices after the reconnect: %+v", notices)
	}
	if again := b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-bob"}).Messages; len(again) != 0 {
		t.Fatalf("notice repeated: %+v", again)
	}
	if got := b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages; len(got) != 1 || got[0].Subject != "while you were gone" {
		t.Fatalf("inbox: %+v", got)
	}
}

// A session that stopped and comes back inside the time its inbox is kept gets the inbox, with
// what was sent to it while it was stopped, and its start is announced again, since the stop was.
func TestStoppedSessionComesBackToItsInbox(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newTimedDevice(t, remote, 300*time.Millisecond, time.Hour)
	bob, conn := b.session("bbbbbb", "")
	eventually(t, "presence", func() bool { return a.seesSession(bob) })
	a.do(proto.Request{Op: proto.OpSend, To: bob, Subject: "before it stopped"})
	eventually(t, "delivery", func() bool { return logHas(b, "before it stopped") })

	conn.Close()
	eventually(t, "bob stopped", func() bool { return busLines(b, bob, "stopped") == 1 && !a.seesSession(bob) })
	if r := a.do(proto.Request{Op: proto.OpSend, To: bob, Subject: "while it was stopped"}); r.Error != "" {
		t.Fatalf("send to a stopped session: %+v", r)
	}
	eventually(t, "delivery", func() bool { return logHas(b, "while it was stopped") })

	if _, err := b.restartedMCP("bbbbbb", ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "bob's start announced again", func() bool { return busLines(b, bob, "started") == 2 })
	eventually(t, "bob back on a", func() bool { return a.seesSession(bob) })
	var got []string
	for _, m := range b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages {
		got = append(got, m.Subject)
	}
	if strings.Join(got, ",") != "before it stopped,while it was stopped" {
		t.Fatalf("inbox: %q", got)
	}
}

// A stopped session's inbox is in state.json like the others, so it survives a daemon restart,
// and so does what was sent to the session while the daemon was down.
func TestStoppedInboxSurvivesDaemonRestart(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newTimedDevice(t, remote, 300*time.Millisecond, time.Hour)
	bob, conn := b.session("bbbbbb", "")
	eventually(t, "presence", func() bool { return a.seesSession(bob) })
	a.do(proto.Request{Op: proto.OpSend, To: bob, Subject: "before it stopped"})
	eventually(t, "delivery", func() bool { return logHas(b, "before it stopped") })
	conn.Close()
	eventually(t, "bob stopped", func() bool { return busLines(b, bob, "stopped") == 1 && !a.seesSession(bob) })
	a.do(proto.Request{Op: proto.OpSend, To: bob, Subject: "while it was stopped"})
	eventually(t, "delivery", func() bool { return logHas(b, "while it was stopped") })

	b.stop()
	a.do(proto.Request{Op: proto.OpSend, To: bob, Subject: "while the daemon was down"})
	b.start()
	eventually(t, "the daemon reads what it missed", func() bool { return logHas(b, "while the daemon was down") })

	if _, err := b.restartedMCP("bbbbbb", ""); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages {
		got = append(got, m.Subject)
	}
	if want := "before it stopped,while it was stopped,while the daemon was down"; strings.Join(got, ",") != want {
		t.Fatalf("inbox: %q, want %s", got, want)
	}
}

// storedFor says whether state.json holds an inbox, or a stopped record, for session id.
func storedFor(t *testing.T, d *device, id string) bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(d.gitDir, brand.StateDir, "state.json"))
	if err != nil {
		return true // being replaced; look again
	}
	var st struct {
		Inbox   map[string]json.RawMessage `json:"inbox"`
		Stopped map[string]json.RawMessage `json:"stopped"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatalf("state.json: %v", err)
	}
	_, inbox := st.Inbox[id]
	_, stopped := st.Stopped[id]
	return inbox || stopped
}

// Past the time it is kept, a stopped session's inbox is deleted, and a session of that ID
// registering after that starts with nothing.
func TestStoppedInboxExpires(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newTimedDevice(t, remote, 200*time.Millisecond, 8*time.Second)
	bob, conn := b.session("bbbbbb", "")
	eventually(t, "presence", func() bool { return a.seesSession(bob) })
	a.do(proto.Request{Op: proto.OpSend, To: bob, Subject: "never read"})
	eventually(t, "delivery", func() bool { return logHas(b, "never read") })
	conn.Close()
	eventually(t, "bob stopped", func() bool { return busLines(b, bob, "stopped") == 1 })

	holds(t, "bob's inbox kept", 2*time.Second, func() bool { return storedFor(t, b, "bbbbbb") })
	eventually(t, "bob's inbox deleted", func() bool { return !storedFor(t, b, "bbbbbb") })

	if _, err := b.restartedMCP("bbbbbb", ""); err != nil {
		t.Fatal(err)
	}
	if got := b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages; len(got) != 0 {
		t.Fatalf("inbox after the time ran out: %+v", got)
	}
}
