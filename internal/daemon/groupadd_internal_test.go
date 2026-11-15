package daemon

import (
	"context"
	"log"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/tags"
)

// A group a person took a session out of is left out of an agent's add of it, with a line in the
// log, and the rest are given: kept as the session's own under the person's mark, it would only
// take one of its 12 places (#543).
func TestAnAddLeavesOutAGroupAPersonTookTheSessionOutOf(t *testing.T) {
	t.Parallel()
	d := tagsDaemon(t)
	var out strings.Builder
	d.log = log.New(&out, "", 0)
	d.st.Tags["name-bbbbbb"] = &tags.State{Human: map[string]tags.Mark{"group:ui": {On: false, By: "tyler", At: time.Now()}}}
	d.mu.Lock()
	d.applyGroupAddLocked(bus.Event{V: bus.Version, ID: "abcdef12", Type: bus.GroupAdd, From: "far/ffffff",
		To: "dev/bbbbbb", At: time.Now(), Add: []string{"group:ui", "group:bus"}}, modeNormal)
	self := d.st.Tags["name-bbbbbb"].Self
	d.mu.Unlock()
	if !slices.Equal(self, []string{"group:bus"}) {
		t.Errorf("the session's own tags are %q; want group:bus alone", self)
	}
	if want := "far/ffffff added dev/bbbbbb to group:ui, but a person took that session out of it; not applied"; !strings.Contains(out.String(), want) {
		t.Errorf("logged %q; want %q", out.String(), want)
	}
}

// An add for a session on a device whose clock runs an hour or more ahead of this one's is refused
// to the adder: that device would take the add, dated by this clock, as too old to apply (#543). A
// clock a little ahead doesn't matter.
func TestAnAddForADeviceWhoseClockRunsAnHourAheadIsRefused(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		ahead   time.Duration
		refused bool
	}{{2 * time.Hour, true}, {20 * time.Minute, false}} {
		d := tagsDaemon(t)
		d.peers["far"] = &peer{lastChange: time.Now(), pres: bus.Presence{V: bus.Version, Device: "far", At: time.Now().Add(tc.ahead),
			Groups: true, Sessions: []bus.SessionInfo{{ID: "ffffff", Agent: "claude"}}}}
		// No team key here, so an add that passes the checks fails to go out, at once.
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		r := d.addToGroups(ctx, "aaaaaa", proto.Request{Add: []string{"group:ui"}, For: []string{"far/ffffff"}})
		cancel()
		t.Logf("%s ahead: %s", tc.ahead, r.Error)
		if got := strings.Contains(r.Error, "clock runs at least"); got != tc.refused {
			t.Errorf("a clock %s ahead: told %q; want it refused for the clock %v", tc.ahead, r.Error, tc.refused)
		}
	}
}
