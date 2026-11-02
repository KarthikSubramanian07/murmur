package bus_test

import (
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
)

// The registry is writable by anyone with push access, so what a reader takes from it is held to
// what Murmur writes: names names.Generate could make, addresses in their own alphabets, this
// version.
func TestNameRecordClaims(t *testing.T) {
	at := time.Now().UTC()
	cases := []struct {
		what string
		rec  bus.NameRecord
		want map[string]string
	}{
		{"an assignment", bus.NameRecord{V: bus.Version, Name: "quiet-otter", Device: "tyler-3f2a", Session: "s1", At: at},
			map[string]string{"quiet-otter": "tyler-3f2a/s1"}},
		{"a snapshot", bus.NameRecord{V: bus.Version, At: at, Used: map[string]string{"quiet-otter": "tyler-3f2a/s1", "limber-geode": ""}},
			map[string]string{"quiet-otter": "tyler-3f2a/s1", "limber-geode": ""}},
		{"an assignment and a snapshot, the assignment's address kept",
			bus.NameRecord{V: bus.Version, Name: "quiet-otter", Device: "tyler-3f2a", Session: "s1", At: at,
				Used: map[string]string{"quiet-otter": "other-0001/x", "limber-geode": ""}},
			map[string]string{"quiet-otter": "tyler-3f2a/s1", "limber-geode": ""}},
		{"a name names.Generate couldn't make", bus.NameRecord{V: bus.Version, Name: "Quiet Otter", Device: "tyler-3f2a", Session: "s1", At: at,
			Used: map[string]string{"quiet": "", "quiet-otter-extra": "", "QUIET-OTTER": ""}},
			map[string]string{}},
		{"an address in the wrong alphabet is dropped, the name kept",
			bus.NameRecord{V: bus.Version, Name: "quiet-otter", Device: "Tyler 3f2a", Session: "s1", At: at,
				Used: map[string]string{"limber-geode": "../etc/passwd"}},
			map[string]string{"quiet-otter": "", "limber-geode": ""}},
		{"another version", bus.NameRecord{V: bus.Version + 1, Name: "quiet-otter", Device: "tyler-3f2a", Session: "s1", At: at},
			map[string]string{}},
	}
	for _, c := range cases {
		got := c.rec.Claims()
		if len(got) != len(c.want) {
			t.Errorf("%s: claims %v, want %v", c.what, got, c.want)
			continue
		}
		for name, addr := range c.want {
			if a, ok := got[name]; !ok || a != addr {
				t.Errorf("%s: claims %v, want %v", c.what, got, c.want)
			}
		}
	}
}

// A record survives the bus's encoding, and its subject says what it is.
func TestNameRecordRoundTrip(t *testing.T) {
	rec := bus.NameRecord{V: bus.Version, Name: "quiet-otter", Device: "tyler-3f2a", Session: "s1", At: time.Now().UTC()}
	msg, err := bus.Encode(rec, rec.Subject())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(msg, "quiet-otter -> tyler-3f2a/s1\n\n") {
		t.Fatalf("message %q", msg)
	}
	var back bus.NameRecord
	if err := bus.Decode(msg, &back); err != nil {
		t.Fatal(err)
	}
	if got := back.Claims(); len(got) != 1 || got["quiet-otter"] != "tyler-3f2a/s1" {
		t.Fatalf("claims %v", got)
	}
	snap := bus.NameRecord{V: bus.Version, Used: map[string]string{"a-b": "", "quiet-otter": ""}}
	if got := snap.Subject(); got != "snapshot of 2 names" {
		t.Fatalf("subject %q", got)
	}
}

// A reservation sets a name aside for a device with no session yet. A reader that predates
// reservations sees in it, through Claims, a name in use by a session it doesn't know, so it never
// hands the name out; a reader that knows them learns the device.
func TestNameReservation(t *testing.T) {
	rec := bus.NameRecord{V: bus.Version, Name: "quiet-otter", Device: "tyler-3f2a", At: time.Now().UTC()}
	if got := rec.Claims(); len(got) != 1 || got["quiet-otter"] != "" {
		t.Fatalf("claims %v, want quiet-otter in use with no address", got)
	}
	if name, dev, ok := rec.Reservation(); !ok || name != "quiet-otter" || dev != "tyler-3f2a" {
		t.Fatalf("reservation %q %q %v", name, dev, ok)
	}
	if got := rec.Subject(); got != "quiet-otter set aside for tyler-3f2a" {
		t.Fatalf("subject %q", got)
	}
	for _, r := range []bus.NameRecord{
		{V: bus.Version, Name: "quiet-otter", Device: "tyler-3f2a", Session: "s1"}, // an assignment
		{V: bus.Version, Name: "quiet-otter", Device: "Tyler 3f2a"},                // not a device
		{V: bus.Version, Name: "Quiet Otter", Device: "tyler-3f2a"},                // not a name
		{V: bus.Version + 1, Name: "quiet-otter", Device: "tyler-3f2a"},            // another version
	} {
		if _, _, ok := r.Reservation(); ok {
			t.Errorf("%+v read as a reservation", r)
		}
	}
}
