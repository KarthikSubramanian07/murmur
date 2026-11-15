package daemon

import (
	"io"
	"log"
	"strings"
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

// An add refused as too old says how old it was by this device's clock, beside its time by its
// sender's, so a clock far off on either side shows (#543).
func TestAnAddTooOldToApplySaysHowOld(t *testing.T) {
	t.Parallel()
	d := tagsDaemon(t)
	var out strings.Builder
	d.log = log.New(&out, "", 0)
	at := time.Now().Add(-2 * time.Hour)
	d.mu.Lock()
	d.applyGroupAddLocked(bus.Event{V: bus.Version, ID: "abcdef12", Type: bus.GroupAdd, From: "far/xxxxxx",
		To: "dev/aaaaaa", At: at, Add: []string{"group:ui"}}, modeNormal)
	d.mu.Unlock()
	want := "at " + at.Format(time.RFC3339) + " by its clock, 2h0m0s ago by this device's, too long ago to apply"
	if !strings.Contains(out.String(), want) {
		t.Errorf("logged %q; want it to say %q", out.String(), want)
	}
}
