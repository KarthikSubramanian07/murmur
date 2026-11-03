package daemon

import (
	"io"
	"log"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
)

// The name registry is one ref every device writes, so a record is read only if the device that
// sealed it may write it: names for itself after a parent, anyone's in a new root (a repair). A
// record with no seal is refused for a device that seals, and read for one that doesn't (#155).
func TestNameRecordSeals(t *testing.T) {
	keyA, keyB := bus.NewKey(), bus.NewKey()
	d := &Daemon{dir: t.TempDir(), log: log.New(io.Discard, "", 0),
		st:       state{Keys: map[string]string{"dev-a": bus.PublicKey(keyA), "dev-b": bus.PublicKey(keyB)}},
		unsealed: map[string]bool{}, forged: map[string]time.Time{}}
	rec := func(dev string) bus.NameRecord {
		return bus.NameRecord{V: bus.Version, Name: "quiet-otter", Device: dev, Session: "s1", At: time.Now().UTC()}
	}
	sealed := func(r bus.NameRecord, k []byte, by, parent string) bus.Commit {
		msg, err := bus.EncodeSigned(r, r.Subject(), k, by, brand.NamesRef, parent)
		if err != nil {
			t.Fatal(err)
		}
		return bus.Commit{Parents: parent, Message: msg}
	}
	unsealed := func(r bus.NameRecord, parent string) bus.Commit {
		msg, err := bus.Encode(r, r.Subject())
		if err != nil {
			t.Fatal(err)
		}
		return bus.Commit{Parents: parent, Message: msg}
	}
	for _, c := range []struct {
		name string
		rec  bus.NameRecord
		c    bus.Commit
		want bool
	}{
		{"its own name, sealed", rec("dev-a"), sealed(rec("dev-a"), keyA, "dev-a", "p1"), true},
		{"another's name, after a parent", rec("dev-b"), sealed(rec("dev-b"), keyA, "dev-a", "p1"), false},
		{"another's name, in a new root", rec("dev-b"), sealed(rec("dev-b"), keyA, "dev-a", ""), true},
		{"sealed with the wrong key", rec("dev-a"), sealed(rec("dev-a"), keyB, "dev-a", "p1"), false},
		{"unsealed, for a device that seals", rec("dev-a"), unsealed(rec("dev-a"), "p1"), false},
		{"unsealed, for an older device", rec("old-0001"), unsealed(rec("old-0001"), "p1"), true},
		{"unsealed, a new root", rec("dev-a"), unsealed(rec("dev-a"), ""), true},
	} {
		if got := d.nameSealOK(c.rec, c.c); got != c.want {
			t.Errorf("%s: read %v, want %v", c.name, got, c.want)
		}
	}
}
