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
	d.newSessionLocked(&proto.Hello{ID: "bbbbbb"}, "claude-code", "")
	d.newSessionLocked(&proto.Hello{ID: "cccccc"}, "claude-code", "")
	d.deliverLocked(msg("bbbbbb"), modeCatchUp)
	d.deliverLocked(msg("cccccc"), modeCatchUp)
	if n := len(d.st.Inbox["bbbbbb"]); n != 1 {
		t.Errorf("the returned session's inbox has %d messages, want the one sent while it was stopped", n)
	}
	if n := len(d.st.Inbox["cccccc"]); n != 0 {
		t.Errorf("a session new here got %d messages sent before it started", n)
	}
}
