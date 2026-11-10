package daemon

import (
	"errors"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
)

// keyRig is a daemon with only what the team key's rules need: one peer, pinned, and one epoch whose
// key this device holds.
func keyRig(t *testing.T) (*Daemon, *peer) {
	t.Helper()
	d := &Daemon{opt: Options{StaleAfter: time.Minute, StoppedInbox: 24 * time.Hour}, device: "me-0001",
		dir: t.TempDir(), log: log.New(io.Discard, "", 0), boxKey: bus.NewBoxKey(), peers: map[string]*peer{},
		teamKeys: bus.Keyring{}, epochSeen: map[string]time.Time{}, teamChanged: make(chan struct{}),
		pollWake: make(chan struct{}, 1), kick: make(chan struct{}, 1),
		st: state{Pins: map[string]*keyPin{}, TeamGranted: map[string][]string{}}}
	d.st.Pins["peer-0001"] = &keyPin{Sign: "s", Box: bus.BoxPublic(bus.NewBoxKey())}
	p := &peer{pres: bus.Presence{V: bus.Version, Device: "peer-0001"}}
	d.peers["peer-0001"] = p
	return d, p
}

// withEpoch gives the rig an epoch, known since seen, and its key if held.
func withEpoch(d *Daemon, seen time.Time, held bool) teamEpoch {
	k := bus.NewBoxKey()
	ep := teamEpoch{ID: bus.TeamID(bus.BoxPublic(k)), Pub: bus.BoxPublic(k), By: d.device, At: time.Now().UTC()}
	d.st.Epochs = append(d.st.Epochs, ep)
	d.epochSeen[ep.ID] = seen
	if held {
		d.teamKeys[ep.ID] = k
	}
	return ep
}

// A device is granted the team keys once this daemon has watched it publish a sealed presence after
// the epoch began, lately: a presence from before the epoch, or one that sat on its ref, is no proof
// it can still push, which is what keeps a removed collaborator from a new epoch.
func TestGrantNeedsAPresenceWatchedAfterTheEpoch(t *testing.T) {
	d, p := keyRig(t)
	ep := withEpoch(d, time.Now().Add(-30*time.Second), true)
	if due := d.grantsDueLocked(); len(due) != 0 {
		t.Fatalf("granted with no presence watched: %v", due)
	}
	p.watched = time.Now().Add(-time.Minute / 2).Add(-time.Second) // before the epoch was known
	if due := d.grantsDueLocked(); len(due) != 0 {
		t.Fatalf("granted on a presence from before the epoch: %v", due)
	}
	p.watched = time.Now()
	if due := d.grantsDueLocked(); len(due) != 1 || due[0] != "peer-0001" {
		t.Fatalf("not granted on a presence after the epoch: %v", due)
	}
	d.st.TeamGranted["peer-0001"] = []string{ep.ID}
	if due := d.grantsDueLocked(); len(due) != 0 {
		t.Fatalf("granted twice: %v", due)
	}
	delete(d.st.TeamGranted, "peer-0001")
	d.epochSeen[ep.ID] = time.Now().Add(-2 * time.Minute)
	p.watched = time.Now().Add(-90 * time.Second)
	if due := d.grantsDueLocked(); len(due) != 0 {
		t.Fatalf("granted on a presence older than StaleAfter: %v", due)
	}
}

// Only a device holding the current key grants, and only to a device with a pinned box key: one on a
// Murmur from before encryption has none.
func TestGrantNeedsTheKeyAndAPin(t *testing.T) {
	d, p := keyRig(t)
	withEpoch(d, time.Now().Add(-time.Minute), false)
	p.watched = time.Now()
	if due := d.grantsDueLocked(); len(due) != 0 {
		t.Fatalf("granted a key this device doesn't hold: %v", due)
	}
	d2, p2 := keyRig(t)
	withEpoch(d2, time.Now().Add(-time.Minute), true)
	p2.watched = time.Now()
	d2.st.Pins["peer-0001"].Box = ""
	if due := d2.grantsDueLocked(); len(due) != 0 {
		t.Fatalf("granted a device with no box key: %v", due)
	}
}

// A record is encrypted for the current team key alone; before there is one, nothing goes out. A
// message to a device on a Murmur from before encryption is refused.
func TestEncryptForTheTeamKey(t *testing.T) {
	d, _ := keyRig(t)
	msg := bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message, From: "me-0001/aaaaaa", To: "*", Subject: "x"}
	if _, err := d.encryptEventLocked(msg); !errors.Is(err, errNoTeamKey) {
		t.Fatalf("encrypted with no team key: %v", err)
	}
	withEpoch(d, time.Now(), false)
	ep := withEpoch(d, time.Now(), false) // a rotation: the newest is current
	boxed, err := d.encryptEventLocked(msg)
	if err != nil {
		t.Fatal(err)
	}
	if got := boxed.Box.Recipients(); len(got) != 1 || got[0] != ep.ID {
		t.Fatalf("encrypted for %v, want only %s", got, ep.ID)
	}
	d.peers["old-0001"] = &peer{pres: bus.Presence{V: bus.Version, Device: "old-0001"}}
	msg.To = "old-0001/bbbbbb"
	if _, err := d.encryptEventLocked(msg); err == nil || !strings.Contains(err.Error(), "from before the bus was encrypted") {
		t.Fatalf("a message to an older device: %v", err)
	}
}

// A box for an epoch this device hasn't been granted makes the log wait; one for a key the team ref
// doesn't hold is junk, passed over; one for a key it holds opens.
func TestOpenWaitsForAGrant(t *testing.T) {
	d, _ := keyRig(t)
	ep := withEpoch(d, time.Now(), false)
	e := bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message, From: "peer-0001/aaaaaa", To: "*", Subject: "x", At: time.Now().UTC()}
	boxed, err := bus.EncryptEvent(e, "peer-0001", map[string]string{ep.ID: ep.Pub})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, wait := d.openEventLocked(boxed, "peer-0001", "ref", "sha"); ok || !wait {
		t.Fatalf("ok %v, wait %v: want a wait", ok, wait)
	}
	stray := bus.NewBoxKey()
	junk, _ := bus.EncryptEvent(e, "peer-0001", map[string]string{bus.TeamID(bus.BoxPublic(stray)): bus.BoxPublic(stray)})
	if _, ok, wait := d.openEventLocked(junk, "peer-0001", "ref", "sha"); ok || wait {
		t.Fatalf("ok %v, wait %v: want junk passed over", ok, wait)
	}
	d2, _ := keyRig(t)
	ep2 := withEpoch(d2, time.Now(), true)
	boxed2, _ := bus.EncryptEvent(e, "peer-0001", map[string]string{ep2.ID: ep2.Pub})
	if in, ok, _ := d2.openEventLocked(boxed2, "peer-0001", "ref", "sha"); !ok || in.Subject != "x" {
		t.Fatalf("didn't open with the key held: %+v", in)
	}
}

// Only a presence signed later than every other taken from the device is new: one put back on its
// ref, or the same one wrapped in a new commit, renews nothing, after a restart included (Sol's
// second review of #156).
func TestOnlyALaterPresenceIsNew(t *testing.T) {
	d, _ := keyRig(t)
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
