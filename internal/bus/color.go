package bus

import (
	"cmp"
	"slices"
	"time"
)

// Colors is how many colours the page's palette has for devices (#560, #573). A device's faces are
// filled with one, so a stack of faces says whose machines they are.
const Colors = 12

// ColorPick is what a device's presence says of its colour: the one its user picked, 1 to Colors,
// and when; Color 0 picked none. Seen is when that presence was written, by the device's clock
// (Presence.At); zero is a device taken to be here.
type ColorPick struct {
	Device string
	Color  int
	At     time.Time
	Seen   time.Time
}

// DeviceColor is the colour a device's faces are shown in: Color is a place in the palette, 0 to
// Colors-1. Picked says it is the colour its user picked; otherwise it was chosen for them.
type DeviceColor struct {
	Color  int
	Picked bool
}

// DeviceColors gives each device its colour, the same on every page that knows the same devices
// (#573). A colour picked by one device only is that device's. Two devices that picked one colour,
// each before it saw the other's, are settled by when they picked it, by their own clocks: the
// first keeps it, and on a tie the lower ID; the other is given a colour as though it had picked
// none, and keeps its pick, which is its own again when the first lets the colour go. A device
// with no colour of its own gets the one its ID hashes to, unless someone picked that one, when it
// gets the next nobody picked; a palette of picked colours leaves it its hash's.
//
// A pick made more than stale after the holder's presence was written takes the colour from it
// (#581): its device had gone quiet, and one that died without a goodbye would hold it for good.
// The two times are both in presence, so every page that reads the same presence settles it the
// same, whatever its own clock says.
func DeviceColors(devs []ColorPick, stale time.Duration) map[string]DeviceColor {
	byFirst := slices.Clone(devs)
	slices.SortFunc(byFirst, func(a, b ColorPick) int {
		return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.Device, b.Device))
	})
	holder := make(map[int]ColorPick)
	for _, p := range byFirst {
		c := p.Color - 1
		if c < 0 || c >= Colors {
			continue
		}
		if h, ok := holder[c]; !ok || ColorGoneQuiet(h, p.At, stale) {
			holder[c] = p
		}
	}
	out := make(map[string]DeviceColor, len(devs))
	held := make(map[int]bool)
	for c, p := range holder {
		held[c] = true
		out[p.Device] = DeviceColor{Color: c, Picked: true}
	}
	for _, p := range devs {
		if _, ok := out[p.Device]; ok {
			continue
		}
		c := autoColor(p.Device)
		for i := 0; i < Colors && held[c]; i++ {
			c = (c + 1) % Colors
		}
		if held[c] {
			c = autoColor(p.Device)
		}
		out[p.Device] = DeviceColor{Color: c}
	}
	return out
}

// ColorGoneQuiet says a pick made at "at" takes holder h's colour (DeviceColors): h's presence
// was written more than stale before it.
func ColorGoneQuiet(h ColorPick, at time.Time, stale time.Duration) bool {
	return !h.Seen.IsZero() && at.Sub(h.Seen) > stale
}

// autoColor is the colour device id hashes to, as the page chose it before colours could be
// picked (#560).
func autoColor(id string) int {
	n := 0
	for _, r := range id {
		n = (n*31 + int(r)) % 9973
	}
	return n % Colors
}
