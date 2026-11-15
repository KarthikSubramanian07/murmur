package daemon

import (
	"io"
	"log"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
)

// An add's age goes by this device's clock, less how far ahead of it the sender's device's clock
// was last measured to be (#543), however long ago that was. With no measure, as for a sender on
// this device or one whose presence was only read at first sight, this device's clock decides.
func TestAnAddsAgeGoesByItsSendersClock(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		what   string
		peer   *peer // the adder's device, there-0002
		from   string
		sent   time.Time // by the adder's clock
		within bool      // within groupAddMaxAge
	}{
		{"a clock two hours behind, a moment ago", &peer{skew: -2 * time.Hour, skewKnown: true},
			"there-0002/aaaaaa", now.Add(-2*time.Hour - 5*time.Second), true},
		{"a clock two hours ahead, two hours ago", &peer{skew: 2 * time.Hour, skewKnown: true},
			"there-0002/aaaaaa", now, false},
		{"a right clock, its device quiet for hours, two hours ago", &peer{skewKnown: true, lastChange: now.Add(-3 * time.Hour)},
			"there-0002/aaaaaa", now.Add(-2 * time.Hour), false},
		{"read only at first sight, by this clock", &peer{},
			"there-0002/aaaaaa", now.Add(-2 * time.Hour), false},
		{"from this device", nil, "here-0001/bbbbbb", now.Add(-time.Minute), true},
	} {
		d := &Daemon{device: "here-0001", peers: map[string]*peer{}}
		if c.peer != nil {
			d.peers["there-0002"] = c.peer
		}
		ago := d.sentAgoLocked(bus.Event{From: c.from, At: c.sent})
		if within := ago <= groupAddMaxAge; within != c.within {
			t.Errorf("%s: %s ago; want within the limit %v", c.what, ago, c.within)
		}
	}
}

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
