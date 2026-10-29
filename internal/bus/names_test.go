package bus_test

import (
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/t4/internal/bus"
)

// The registry is writable by anyone with push access, so what a reader takes from it is held to
// what t4 writes: names names.Generate could make, addresses in their own alphabets, this version.
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
