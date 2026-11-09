package daemon

import (
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
)

// keyRig is a daemon with only what deciding whom to encrypt for needs: one peer, pinned.
func keyRig(t *testing.T) (*Daemon, *peer) {
	t.Helper()
	d := &Daemon{opt: Options{StaleAfter: time.Minute, StoppedInbox: 24 * time.Hour}, device: "me-0001",
		boxKey: bus.NewBoxKey(), peers: map[string]*peer{}, st: state{Pins: map[string]*keyPin{}}}
	d.st.Pins["peer-0001"] = &keyPin{Sign: "s", Box: bus.BoxPublic(bus.NewBoxKey())}
	p := &peer{pres: bus.Presence{V: bus.Version, Device: "peer-0001"}}
	d.peers["peer-0001"] = p
	return d, p
}

// A device is in the team for as long as a sealed presence of its was taken within StaleAfter, by
// this machine's clock now: not as of the last successful sync, which stops moving while this
// daemon is offline, so that one offline for hours doesn't write for a device whose user has since
// lost access (Sol's review of #156).
func TestTeamIsJudgedByNowNotTheLastSync(t *testing.T) {
	d, p := keyRig(t)
	p.verified = time.Now()
	d.lastPollOK = time.Now()
	if _, ok := d.teamKeysLocked()["peer-0001"]; !ok {
		t.Fatal("a peer verified just now is left out")
	}
	// Offline since: the last sync is as old as the presence, which the display doesn't call stale.
	p.verified = time.Now().Add(-2 * time.Minute)
	p.lastChange = p.verified
	d.lastPollOK = p.verified
	if d.staleLocked(p) {
		t.Fatal("rig: the display should still show the peer")
	}
	if _, ok := d.teamKeysLocked()["peer-0001"]; ok {
		t.Fatal("encrypted for a peer last verified longer ago than StaleAfter")
	}
	if _, err := d.encryptEventLocked(bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message, From: "me-0001/aaaaaa",
		To: "*", Subject: "x"}); err != nil {
		t.Fatal(err)
	}
}

// What can't be read, or isn't the device's, moves its presence for display and counts for nothing
// toward the team: anyone who can push could otherwise keep a device that has gone in it.
func TestUnverifiedPresenceDoesNotKeepADeviceInTheTeam(t *testing.T) {
	d, p := keyRig(t)
	p.verified = time.Now().Add(-2 * time.Minute)
	d.sawPresence("peer-0001", "0123456789abcdef", true)
	if p.lastChange.IsZero() || time.Since(p.lastChange) > time.Second {
		t.Fatal("rig: the unreadable presence should count as a sign of life for display")
	}
	if _, ok := d.teamKeysLocked()["peer-0001"]; ok {
		t.Fatal("an unreadable presence kept the device in the team")
	}
}

// A presence dated ahead of this machine's clock counts as never verified when first read; one
// dated within the allowance, or in the past, by its date.
func TestFirstVerifiedDistrustsTheFuture(t *testing.T) {
	if v := firstVerified(time.Now().Add(time.Hour)); !v.IsZero() {
		t.Fatalf("a presence dated an hour ahead verified at %v", v)
	}
	if v := firstVerified(time.Now().Add(30 * time.Second)); v.IsZero() || v.After(time.Now()) {
		t.Fatalf("within the allowance: %v", v)
	}
	past := time.Now().Add(-10 * time.Minute)
	if v := firstVerified(past); v.Sub(past).Abs() > time.Second {
		t.Fatalf("a past date: %v, want %v", v, past)
	}
}

// The 24 hours a message addressed to a device may still be encrypted for it count from when this
// daemon took a presence of the device it hadn't taken before: reading the same one again, as after
// a restart, renews nothing.
func TestSeenIsRenewedOnlyByANewPresence(t *testing.T) {
	d, _ := keyRig(t)
	pin := d.st.Pins["peer-0001"]
	old := time.Now().Add(-23 * time.Hour)
	d.noteSeenLocked("peer-0001", "aaaa", old)
	if pin.Seen.Sub(old).Abs() > time.Second || pin.SHA != "aaaa" {
		t.Fatalf("pin after the first presence: %+v", pin)
	}
	d.noteSeenLocked("peer-0001", "aaaa", time.Now())
	if pin.Seen.Sub(old).Abs() > time.Second {
		t.Fatal("reading the same presence again renewed it")
	}
	d.noteSeenLocked("peer-0001", "bbbb", time.Time{})
	if pin.SHA != "aaaa" {
		t.Fatal("a presence never verified renewed it")
	}
	d.noteSeenLocked("peer-0001", "bbbb", time.Now())
	if time.Since(pin.Seen) > time.Second || pin.SHA != "bbbb" {
		t.Fatalf("a new presence didn't renew it: %+v", pin)
	}
	// Past the 24 hours, a message addressed to the device is refused, not sent where it can't be
	// read or might be read by a device no longer on the team.
	pin.Seen = time.Now().Add(-25 * time.Hour)
	if _, err := d.encryptEventLocked(bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message, From: "me-0001/aaaaaa",
		To: "peer-0001/bbbbbb", Subject: "x"}); err == nil {
		t.Fatal("encrypted for a device last seen 25 hours ago")
	}
}
