package daemon

import (
	"io"
	"log"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
)

// keyRig is a daemon with only what deciding whom to encrypt for needs: one peer, pinned.
func keyRig(t *testing.T) (*Daemon, *peer) {
	t.Helper()
	d := &Daemon{opt: Options{StaleAfter: time.Minute, StoppedInbox: 24 * time.Hour}, device: "me-0001",
		dir: t.TempDir(), log: log.New(io.Discard, "", 0),
		boxKey: bus.NewBoxKey(), peers: map[string]*peer{}, seenAt: map[string]time.Time{}, st: state{Pins: map[string]*keyPin{}}}
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

// Only a presence signed later than every other taken from the device is new: one put back on its
// ref, or the same one wrapped in a new commit, renews nothing, after a restart included (Sol's
// second review of #156).
func TestOnlyALaterPresenceIsNew(t *testing.T) {
	d, p := keyRig(t)
	t0 := time.Now().Add(-time.Minute).UTC()
	if !d.newPresenceLocked("peer-0001", t0) {
		t.Fatal("the first presence isn't new")
	}
	if d.newPresenceLocked("peer-0001", t0) || d.newPresenceLocked("peer-0001", t0.Add(-time.Hour)) {
		t.Fatal("the same presence, or an older one, is new")
	}
	if !d.newPresenceLocked("peer-0001", t0.Add(time.Second)) {
		t.Fatal("a later presence isn't new")
	}
	// Not new: nothing is renewed, whether the peer is known in this run or not.
	p.verified, p.sealed = time.Now().Add(-5*time.Minute), true
	d.lastPollOK = time.Now()
	if v := d.verifiedLocked(p, false, time.Now()); !v.Equal(p.verified) {
		t.Fatal("an old presence renewed the peer")
	}
	if v := d.verifiedLocked(nil, false, time.Now()); !v.IsZero() {
		t.Fatal("an old presence read after a restart counts")
	}
}

// A new presence counts from now once this daemon is watching the device's presence change, however
// far ahead its writer's clock runs; the first one in a run counts from when it was signed, and not
// at all if that is in the future.
func TestVerifiedWhenSeenChanging(t *testing.T) {
	d, p := keyRig(t)
	ahead := time.Now().Add(5 * time.Minute)
	if v := d.verifiedLocked(nil, true, ahead); !v.IsZero() {
		t.Fatal("a first presence dated ahead counts")
	}
	p.sealed = true // a sealed presence was taken before, though it counted for nothing
	if v := d.verifiedLocked(p, true, ahead); !v.IsZero() {
		t.Fatal("counted from now before any sync had finished")
	}
	d.lastPollOK = time.Now()
	if v := d.verifiedLocked(p, true, ahead); time.Since(v) > time.Second {
		t.Fatalf("a device whose clock runs ahead never counts: %v", v)
	}
}

// The 24 hours a message addressed to a device may still be encrypted for it count on the monotonic
// clock in a run, and from a time no later than now across a restart, so a clock set back stretches
// nothing.
func TestSeenWindow(t *testing.T) {
	d, _ := keyRig(t)
	pin := d.st.Pins["peer-0001"]
	if d.seenWithinLocked("peer-0001", 24*time.Hour) {
		t.Fatal("never seen, yet within the window")
	}
	d.noteSeenLocked("peer-0001", time.Now().Add(-23*time.Hour))
	if !d.seenWithinLocked("peer-0001", 24*time.Hour) {
		t.Fatal("seen 23 hours ago, not within 24")
	}
	d.seenAt["peer-0001"] = time.Now().Add(-25 * time.Hour) // as if the wall clock said otherwise
	pin.Seen = time.Now().Add(48 * time.Hour)
	if d.seenWithinLocked("peer-0001", 24*time.Hour) {
		t.Fatal("the wall clock stretched the window in a run")
	}
	delete(d.seenAt, "peer-0001") // a restart, with a time ahead of the clock kept
	if !d.seenWithinLocked("peer-0001", 24*time.Hour) {
		t.Fatal("a kept time ahead of now should count as now")
	}
	pin.Seen = time.Now().Add(-25 * time.Hour)
	if d.seenWithinLocked("peer-0001", 24*time.Hour) {
		t.Fatal("seen 25 hours ago before the restart, still within 24")
	}
	if _, err := d.encryptEventLocked(bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message, From: "me-0001/aaaaaa",
		To: "peer-0001/bbbbbb", Subject: "x"}); err == nil {
		t.Fatal("encrypted for a device last seen 25 hours ago")
	}
}

// A device's own presence times never go back, so its teammates take each as new.
func TestOwnPresenceTimesNeverGoBack(t *testing.T) {
	d, _ := keyRig(t)
	d.st.PresenceAt = time.Now().Add(time.Hour).UTC() // the clock was set back an hour since
	first := d.nextPresenceAtLocked()
	if !first.After(time.Now().Add(59 * time.Minute)) {
		t.Fatalf("went back to %v", first)
	}
	if next := d.nextPresenceAtLocked(); !next.After(first) {
		t.Fatalf("%v isn't after %v", next, first)
	}
}
