package daemon

import (
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/t4/internal/bus"
	"github.com/a-Fig/t4/internal/proto"
)

// A session back within StoppedInbox may register again before its daemon's catch-up has read
// every backlog ref, when a fetch failed at start. A message sent to it while it was stopped,
// read only then, is still its, where a session new to this daemon gets only what was sent after
// it started.
func TestReturnedSessionKeepsCatchUp(t *testing.T) {
	d := &Daemon{opt: Options{StoppedInbox: time.Hour}, dir: t.TempDir(), log: log.New(io.Discard, "", 0),
		device: "here-0001", started: time.Now(), sessions: map[string]*session{},
		st: state{Inbox: map[string][]*inMsg{}, Stopped: map[string]time.Time{"bbbbbb": time.Now().Add(-5 * time.Minute)}}}
	msg := func(to string) bus.Event {
		return bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message, From: "peer-0001/aaaaaa",
			To: "here-0001/" + to, Subject: "sent while it was stopped", At: time.Now().Add(-3 * time.Minute)}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.newSessionLocked(&proto.Hello{ID: "bbbbbb"}, "claude-code", "", "bbbbbb")
	d.newSessionLocked(&proto.Hello{ID: "cccccc"}, "claude-code", "", "cccccc")
	d.deliverLocked(msg("bbbbbb"), modeCatchUp)
	d.deliverLocked(msg("cccccc"), modeCatchUp)
	if n := len(d.st.Inbox["bbbbbb"]); n != 1 {
		t.Errorf("the returned session's inbox has %d messages, want the one sent while it was stopped", n)
	}
	if n := len(d.st.Inbox["cccccc"]); n != 0 {
		t.Errorf("a session new here got %d messages sent before it started", n)
	}
	// After a long outage the backlog is skipped, whether or not the session came back first.
	d.deliverLocked(msg("bbbbbb"), modeSkip)
	if n := len(d.st.Inbox["bbbbbb"]); n != 1 {
		t.Errorf("a skip gave the returned session a message: its inbox has %d, want 1", n)
	}
}

// #37 after a restart whose first fetch failed. Conversation X had stopped as one session, and a
// fresh server for X (not a reconnect) registered under another ID and took X's name before the
// backlog was read. What was sent to the stopped session while the daemon was down, which its kept
// inbox would have caught up on, goes on to the session that holds the name now. The holder's own
// start still counts too: in a skip, what was sent after it started is its, as any session's is.
func TestForwardedCatchUpIsJudgedByItsRecipient(t *testing.T) {
	const nameX = "quiet-otter"
	d := &Daemon{opt: Options{StoppedInbox: time.Hour}, dir: t.TempDir(), log: log.New(io.Discard, "", 0),
		device: "here-0001", started: time.Now(), sessions: map[string]*session{},
		st: state{Inbox: map[string][]*inMsg{}, Stopped: map[string]time.Time{"bbbbbb": time.Now().Add(-5 * time.Minute)},
			NameKeys:    map[string]string{nameKey("", "x", "bbbbbb"): nameX},
			NameHolders: map[string]string{nameX: "bbbbbb"}, SessionNames: map[string]string{"bbbbbb": nameX}}}
	sent := func(subject string, ago time.Duration) bus.Event {
		return bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message, From: "peer-0001/aaaaaa",
			To: "here-0001/bbbbbb", Subject: subject, At: time.Now().Add(-ago)}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.newSessionLocked(&proto.Hello{ID: "cccccc", ClaudeSession: "x"}, "claude-code", "", nameKey("", "x", "cccccc"))
	if h := d.st.NameHolders[nameX]; h != "cccccc" {
		t.Fatalf("the fresh session didn't take the name: its holder is %q", h)
	}
	d.deliverLocked(sent("while the daemon was down", 3*time.Minute), modeCatchUp)
	if n := len(d.st.Inbox["cccccc"]); n != 1 {
		t.Fatalf("the holder's inbox has %d messages, want the one the stopped session would have caught up on", n)
	}
	if n := len(d.st.Inbox["bbbbbb"]); n != 0 {
		t.Errorf("the stopped session kept %d messages, which are the holder's", n)
	}
	d.deliverLocked(sent("history", 8*time.Minute), modeSkip) // before bbbbbb stopped, 5 minutes ago
	d.deliverLocked(sent("since the holder started", 0), modeSkip)
	var got []string
	for _, m := range d.st.Inbox["cccccc"] {
		got = append(got, m.Event.Subject)
	}
	if len(got) != 2 || got[1] != "since the holder started" {
		t.Errorf("after a skip the holder has %q, want the catch-up and what was sent since it started", got)
	}
}

// #30. A skip, after a daemon was down longer than catchUpLimit, delivers to a stopped session
// what was sent after it stopped, with the minute's allowance, and before StoppedInbox ran out; to
// everyone else it delivers nothing, and a session that has registered again is judged by when it
// started, as in any skip.
func TestSkipKeepsInboxesOfStoppedSessions(t *testing.T) {
	now := time.Now()
	stopped := now.Add(-30 * time.Minute)
	d := &Daemon{opt: Options{StoppedInbox: time.Hour}, dir: t.TempDir(), log: log.New(io.Discard, "", 0),
		device: "here-0001", started: now, sessions: map[string]*session{},
		st: state{Inbox: map[string][]*inMsg{}, Stopped: map[string]time.Time{"bbbbbb": stopped, "dddddd": stopped}}}
	sent := func(to, subject string, at time.Time) bus.Event {
		return bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message, From: "peer-0001/aaaaaa",
			To: to, Subject: subject, At: at}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.newSessionLocked(&proto.Hello{ID: "dddddd"}, "claude-code", "", "dddddd")
	for _, e := range []bus.Event{
		sent("here-0001/bbbbbb", "after it stopped", now.Add(-20*time.Minute)),
		sent("here-0001/bbbbbb", "within the minute's allowance", stopped.Add(-30*time.Second)),
		sent("here-0001/bbbbbb", "before it stopped", stopped.Add(-2*time.Minute)),
		sent("here-0001/bbbbbb", "after the inbox would have run out", stopped.Add(2*time.Hour)),
		sent("here-0001", "to the device", now.Add(-20*time.Minute)),
		sent("*", "to everyone", now.Add(-20*time.Minute)),
		sent("here-0001/cccccc", "to nobody kept", now.Add(-20*time.Minute)),
		sent("here-0001/dddddd", "to a session that is back", now.Add(-20*time.Minute)),
	} {
		d.deliverLocked(e, modeSkip)
	}
	subjects := func(id string) string {
		var got []string
		for _, m := range d.st.Inbox[id] {
			got = append(got, m.Event.Subject)
		}
		return strings.Join(got, "|")
	}
	if got, want := subjects("bbbbbb"), "after it stopped|within the minute's allowance|to the device|to everyone"; got != want {
		t.Errorf("the stopped session's inbox: %q, want %q", got, want)
	}
	for _, id := range []string{"cccccc", "dddddd", proto.CLISession} {
		if got := subjects(id); got != "" {
			t.Errorf("%s got %q", id, got)
		}
	}
}
