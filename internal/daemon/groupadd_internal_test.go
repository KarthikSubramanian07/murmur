package daemon

import (
	"log"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
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
