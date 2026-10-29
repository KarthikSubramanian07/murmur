package daemon

import (
	"io"
	"log"
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
			NameKeys:    map[string]string{nameKey("x", "bbbbbb"): nameX},
			NameHolders: map[string]string{nameX: "bbbbbb"}, SessionNames: map[string]string{"bbbbbb": nameX}}}
	sent := func(subject string, ago time.Duration) bus.Event {
		return bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message, From: "peer-0001/aaaaaa",
			To: "here-0001/bbbbbb", Subject: subject, At: time.Now().Add(-ago)}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.newSessionLocked(&proto.Hello{ID: "cccccc", ClaudeSession: "x"}, "claude-code", "", nameKey("x", "cccccc"))
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
	d.deliverLocked(sent("history", 3*time.Minute), modeSkip)
	d.deliverLocked(sent("since the holder started", 0), modeSkip)
	var got []string
	for _, m := range d.st.Inbox["cccccc"] {
		got = append(got, m.Event.Subject)
	}
	if len(got) != 2 || got[1] != "since the holder started" {
		t.Errorf("after a skip the holder has %q, want the catch-up and what was sent since it started", got)
	}
}
