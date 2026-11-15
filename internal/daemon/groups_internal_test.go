package daemon

import (
	"io"
	"log"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
)

// What waited for a session that isn't back when the catch-up window closes is let go, and logged.
func TestAChangeNobodyCameBackForIsLetGo(t *testing.T) {
	d := tagsDaemon(t)
	d.log = log.New(io.Discard, "", 0)
	d.started = time.Now()
	d.mu.Lock()
	d.applyTagsEventLocked(bus.Event{V: bus.Version, ID: "abcdef12", Type: bus.Tags, From: "far/cli",
		To: "dev/cccccc", At: time.Now(), Add: []string{"release"}})
	held := len(d.heldTags)
	d.started = time.Now().Add(-time.Hour)
	d.mu.Unlock()
	d.pruneInboxes()
	if held != 1 || len(d.heldTags) != 0 {
		t.Errorf("held %d, then %d once the window closed; want 1, then none", held, len(d.heldTags))
	}
}
