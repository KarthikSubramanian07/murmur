package daemon

import (
	"io"
	"log"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
)

// colorRig is a daemon with only what the colour rules need: a pick of colour 3, settled, and one
// peer that picked the same.
func colorRig(t *testing.T, mine, theirs time.Time, theirSeen time.Time) (*Daemon, *peer) {
	t.Helper()
	d := &Daemon{opt: Options{StaleAfter: 6 * time.Minute}, device: "me-0002", dir: t.TempDir(),
		log: log.New(io.Discard, "", 0), peers: map[string]*peer{}, kick: make(chan struct{}, 1), colorSettled: true}
	d.st.Color, d.st.ColorAt = 3, mine
	p := &peer{pres: bus.Presence{V: bus.Version, Device: "gone-0001", Color: 3, ColorAt: theirs, At: theirSeen}}
	d.peers["gone-0001"] = p
	return d, p
}

// A pick that took the colour of a device gone quiet (#581) is kept, by a daemon that has been
// running and by one that has just started: the quiet device picked it first, but its presence
// was written long before.
func TestAColourTakenFromAQuietDeviceIsKept(t *testing.T) {
	now := time.Now().UTC()
	for _, settled := range []bool{true, false} {
		d, _ := colorRig(t, now.Add(-time.Hour), now.Add(-30*time.Hour), now.Add(-2*time.Hour))
		d.colorSettled = settled
		d.settleColorLocked()
		if d.st.Color != 3 || d.st.ColorLostTo != "" {
			t.Errorf("settled %v: gave it up, %d %q", settled, d.st.Color, d.st.ColorLostTo)
		}
		if c := d.colorsLocked(); !c[d.device].Picked || c[d.device].Color != 2 || c["gone-0001"].Picked {
			t.Errorf("settled %v: colours %+v", settled, c)
		}
	}
	// Taken within the stale limit of its presence, the colour is the first device's.
	d, _ := colorRig(t, now.Add(-time.Hour), now.Add(-30*time.Hour), now.Add(-time.Hour-time.Minute))
	if d.settleColorLocked(); d.st.Color != 0 || d.st.ColorLostTo != "gone-0001" {
		t.Errorf("a pick soon after its presence: kept, %d %q", d.st.Color, d.st.ColorLostTo)
	}
}

// A device whose own presence went quiet, asleep or offline, may have had its colour taken
// meanwhile. About to write a presence again, it leaves its pick out, and the next full sync gives
// the pick up to the device that took it.
func TestADeviceBackFromBeingQuietGivesUpATakenColour(t *testing.T) {
	now := time.Now().UTC()
	d, p := colorRig(t, now.Add(-30*time.Hour), now.Add(-time.Hour), now.Add(-time.Minute))
	d.presenceOut = now.Add(-2 * time.Hour) // asleep since
	d.colorAwayLocked(now)
	if c, _ := d.shownColorLocked(); c != 0 {
		t.Errorf("the presence written on waking carries pick %d", c)
	}
	d.settleColorLocked()
	if d.st.Color != 0 || d.st.ColorLostTo != "gone-0001" {
		t.Errorf("kept a colour taken while it was quiet: %d %q", d.st.Color, d.st.ColorLostTo)
	}

	// Back within the stale limit, nobody could have taken it.
	d, p = colorRig(t, now.Add(-30*time.Hour), now.Add(-time.Hour), now.Add(-time.Minute))
	p.pres.Color = 0
	d.presenceOut = now.Add(-time.Minute)
	if d.colorAwayLocked(now); !d.colorSettled {
		t.Errorf("a presence a minute old unsettled the pick")
	}
}
