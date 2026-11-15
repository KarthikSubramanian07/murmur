package bus

import (
	"testing"
	"time"
)

// Tests of devices' colours (#573): one rule gives each device its colour, the same on every page.

var colorT0 = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

const colorStale = 6 * time.Minute

// A colour two devices picked goes to the one that picked it first, whatever order they are read
// in; the other is given one as though it had picked none.
func TestTheFirstToPickAColourKeepsIt(t *testing.T) {
	a := ColorPick{Device: "ada-mbp-1111", Color: 3, At: colorT0.Add(time.Minute)}
	b := ColorPick{Device: "bob-mbp-2222", Color: 3, At: colorT0}
	for _, devs := range [][]ColorPick{{a, b}, {b, a}} {
		got := DeviceColors(devs, colorStale)
		if got[b.Device] != (DeviceColor{Color: 2, Picked: true}) {
			t.Errorf("the first to pick it: %+v", got[b.Device])
		}
		if c := got[a.Device]; c.Picked || c.Color == 2 {
			t.Errorf("the second: %+v", c)
		}
	}
	// At the same moment, the lower ID keeps it.
	a.At = b.At
	if got := DeviceColors([]ColorPick{b, a}, colorStale); !got[a.Device].Picked || got[b.Device].Picked {
		t.Errorf("a tie: %+v", got)
	}
}

// A device that died without a goodbye leaves its presence and its pick (#581). A pick made more
// than the stale limit after that presence was written takes the colour, read in any order and by
// any clock; one made sooner loses to it as an earlier pick does. A presence with no time known
// is taken to be here.
func TestAPickTakesTheColourOfADeviceThatWentQuiet(t *testing.T) {
	gone := ColorPick{Device: "ada-mbp-1111", Color: 3, At: colorT0, Seen: colorT0.Add(time.Hour)}
	late := ColorPick{Device: "bob-mbp-2222", Color: 3, At: gone.Seen.Add(colorStale + time.Second)}
	for _, devs := range [][]ColorPick{{gone, late}, {late, gone}} {
		got := DeviceColors(devs, colorStale)
		if got[late.Device] != (DeviceColor{Color: 2, Picked: true}) {
			t.Errorf("the pick after it went quiet: %+v", got[late.Device])
		}
		if c := got[gone.Device]; c.Picked || c.Color == 2 {
			t.Errorf("the quiet device: %+v", c)
		}
	}
	soon := late
	soon.At = gone.Seen.Add(colorStale)
	if got := DeviceColors([]ColorPick{soon, gone}, colorStale); !got[gone.Device].Picked || got[soon.Device].Picked {
		t.Errorf("a pick within the stale limit: %+v", got)
	}
	// A third device that picks it while the second is here loses to the second, not the first.
	third := ColorPick{Device: "cat-xps-3333", Color: 3, At: late.At.Add(time.Minute)}
	late.Seen = third.At
	if got := DeviceColors([]ColorPick{third, gone, late}, colorStale); !got[late.Device].Picked || got[third.Device].Picked || got[gone.Device].Picked {
		t.Errorf("three picks: %+v", got)
	}
	gone.Seen = time.Time{}
	if got := DeviceColors([]ColorPick{gone, late}, colorStale); !got[gone.Device].Picked {
		t.Errorf("a presence with no time: %+v", got)
	}
}

// A device that picked none gets the colour its ID hashes to, unless someone picked that one: then
// the next nobody picked.
func TestAutomaticColoursStepAroundPickedOnes(t *testing.T) {
	auto := ColorPick{Device: "cat-xps-3333"}
	own := autoColor(auto.Device)
	if got := DeviceColors([]ColorPick{auto}, colorStale)[auto.Device]; got != (DeviceColor{Color: own}) {
		t.Fatalf("alone: %+v, want colour %d", got, own)
	}
	picks := []ColorPick{auto,
		{Device: "dan-1", Color: own + 1, At: colorT0},
		{Device: "eve-2", Color: (own+1)%Colors + 1, At: colorT0}}
	if got := DeviceColors(picks, colorStale)[auto.Device]; got.Color != (own+2)%Colors || got.Picked {
		t.Errorf("with its colour and the next picked: %+v, want %d", got, (own+2)%Colors)
	}
	// With every colour picked, it keeps its own.
	picks = []ColorPick{auto}
	for c := 1; c <= Colors; c++ {
		picks = append(picks, ColorPick{Device: "dev-" + string(rune('a'+c)), Color: c, At: colorT0})
	}
	if got := DeviceColors(picks, colorStale)[auto.Device]; got.Color != own {
		t.Errorf("with every colour picked: %+v", got)
	}
}

// The page hashed a device's ID to its colour before colours could be picked (#560); the hash is
// the same, so a device that picks none keeps the colour it had.
func TestAutoColourIsThePagesHash(t *testing.T) {
	// From the page's deviceColor before #573, run in Node.
	for id, want := range map[string]int{"tyler-mbp-7a20": 6, "maya-mbp-91c0": 9, "sam-desktop-07be": 5} {
		if got := autoColor(id); got != want {
			t.Errorf("%s: %d, the page had %d", id, got, want)
		}
	}
}

// Clean drops a colour that isn't in the palette, or has no time to settle a race by.
func TestPresenceCleanDropsABadColour(t *testing.T) {
	for _, p := range []Presence{
		{Device: "d-1", Color: Colors + 1, ColorAt: colorT0},
		{Device: "d-1", Color: -1, ColorAt: colorT0},
		{Device: "d-1", Color: 2},
	} {
		p.Clean()
		if p.Color != 0 || !p.ColorAt.IsZero() {
			t.Errorf("kept %d at %v", p.Color, p.ColorAt)
		}
	}
	p := Presence{Device: "d-1", Color: Colors, ColorAt: colorT0}
	if p.Clean(); p.Color != Colors {
		t.Errorf("dropped a good colour")
	}
}
