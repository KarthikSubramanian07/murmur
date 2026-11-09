package daemon_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/proto"
)

// newOwnedDevice is newDevice with git's user.name set in the clone, which presence carries.
func newOwnedDevice(t *testing.T, remote, owner string) *device {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	git(t, filepath.Dir(dir), "clone", "-q", "--no-local", remote, dir)
	git(t, dir, "config", "user.name", owner)
	d := &device{t: t, gitDir: filepath.Join(dir, ".git"), stale: 3 * time.Second, poll: 100 * time.Millisecond}
	d.start()
	return d
}

// remoteObjects is every object in the remote, as one string: what anyone who can read the repo
// can read.
func remoteObjects(t *testing.T, remote string) string {
	t.Helper()
	return git(t, remote, "cat-file", "--batch-all-objects", "--batch")
}

// doing is the status a device's who shows for addr, and whether it lists addr.
func (d *device) doing(addr string) (string, bool) {
	for _, dv := range d.do(proto.Request{Op: proto.OpWho}).Devices {
		for _, s := range dv.Sessions {
			if s.Addr == addr {
				return s.Doing, true
			}
		}
	}
	return "", false
}

// Three devices, one joining after the others are talking: each reads the others' presence and
// messages, and the remote holds none of what they said or are doing.
func TestBusIsEncrypted(t *testing.T) {
	remote := newRemote(t)
	a := newOwnedDevice(t, remote, "Alice Secretowner")
	b := newOwnedDevice(t, remote, "Bob Secretowner")
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.session("bbbbbb", "")
	a.do(proto.Request{Op: proto.OpStatus, Session: "aaaaaa", Doing: "status-alpha-secret"})
	b.do(proto.Request{Op: proto.OpStatus, Session: "bbbbbb", Doing: "status-bravo-secret"})
	eventually(t, "each sees the other's status", func() bool {
		da, _ := b.doing(alice)
		db, _ := a.doing(bob)
		return da == "status-alpha-secret" && db == "status-bravo-secret"
	})

	if r := a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: bob, Subject: "subject-direct-secret", Body: "body-direct-secret"}); r.Error != "" {
		t.Fatalf("send: %+v", r)
	}
	eventually(t, "bob gets it", func() bool {
		in := b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages
		return len(in) == 1 && in[0].Body == "body-direct-secret" && in[0].FromOwner == "Alice Secretowner"
	})

	// A device that joins later reads the others' presence once they see it and push theirs again,
	// and their messages from then on.
	c := newOwnedDevice(t, remote, "Carol Secretowner")
	carol, _ := c.session("cccccc", "")
	eventually(t, "c reads a's and b's presence", func() bool {
		da, _ := c.doing(alice)
		db, _ := c.doing(bob)
		return da == "status-alpha-secret" && db == "status-bravo-secret"
	})
	eventually(t, "a reads c's presence", func() bool { return a.seesSession(carol) })
	if r := b.do(proto.Request{Op: proto.OpSend, Session: "bbbbbb", To: "*", Subject: "subject-broadcast-secret", Body: "body-broadcast-secret"}); r.Error != "" {
		t.Fatalf("broadcast: %+v", r)
	}
	for _, d := range []*device{a, c} {
		eventually(t, "the broadcast", func() bool {
			for _, m := range d.do(proto.Request{Op: proto.OpInbox}).Messages {
				if m.Body == "body-broadcast-secret" {
					return true
				}
			}
			return false
		})
	}

	all := remoteObjects(t, remote)
	// The name registry, which stays in the clear, holds session IDs and names; nothing else does.
	for _, s := range []string{"secret", "Secretowner", `"host"`, `"to"`, `"subject"`, `"doing"`, `"agent"`,
		"started (", " sessions\n"} {
		if strings.Contains(all, s) {
			t.Errorf("the remote holds %q", s)
		}
	}
	// What is readable there: device IDs, box keys, seals.
	if !strings.Contains(all, `"boxKey"`) || !strings.Contains(all, `"seal"`) || !strings.Contains(all, a.self()) {
		t.Errorf("the remote holds no box keys or seals:\n%.2000s", all)
	}
}

// presenceOn reads the presence commit of dev on the remote.
func presenceOn(t *testing.T, remote, dev string) bus.Presence {
	t.Helper()
	var p bus.Presence
	if err := bus.Decode(git(t, remote, "log", "-1", "--format=%B", "refs/murmur/presence/"+dev), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// lastEventOn reads the last event of dev's log on the remote, as written.
func lastEventOn(t *testing.T, remote, dev string) bus.Event {
	t.Helper()
	var e bus.Event
	if err := bus.Decode(git(t, remote, "log", "-1", "--format=%B", "refs/murmur/mesh/"+dev), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// A device from before encryption: it writes in the clear, which an updated device reads, and it
// can't read what an updated device writes, so a message to it is refused rather than sent where
// it can't be read, or sent in the clear.
func TestOlderDevice(t *testing.T) {
	remote := newRemote(t)
	a := newDevice(t, remote)
	alice, _ := a.session("aaaaaa", "")
	const old = "oldhost-0001"
	pushOld := func() {
		pres := bus.Presence{V: bus.Version, Device: old, Host: "oldhost", At: time.Now().UTC(),
			Sessions: []bus.SessionInfo{{ID: "oooooo", Agent: "claude", Started: time.Now().UTC()}}}
		msg, err := bus.Encode(pres, old+": 1 sessions")
		if err != nil {
			t.Fatal(err)
		}
		pushMessage(t, remote, "refs/murmur/presence/"+old, msg)
	}
	pushOld()
	eventually(t, "a sees the older device's session", func() bool { return a.seesSession(old + "/oooooo") })

	pushEvent(t, remote, "refs/murmur/mesh/"+old, bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message,
		From: old + "/oooooo", To: alice, Subject: "from the older device", At: time.Now().UTC()})
	eventually(t, "a reads its plaintext", func() bool {
		in := a.do(proto.Request{Op: proto.OpInbox, Session: "aaaaaa"}).Messages
		return len(in) == 1 && in[0].Subject == "from the older device"
	})

	pushOld() // fresh again, whatever the stale limit did meanwhile
	before := git(t, remote, "rev-parse", "refs/murmur/mesh/"+a.self())
	r := a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: old + "/oooooo", Subject: "can't be read there"})
	if !strings.Contains(r.Error, "not sent") || !strings.Contains(r.Error, "before the bus was encrypted") {
		t.Fatalf("send to an older device: %+v", r)
	}
	if after := git(t, remote, "rev-parse", "refs/murmur/mesh/"+a.self()); after != before {
		t.Fatal("something went out")
	}
	// A broadcast goes out, for the devices that can read it, and not in the clear.
	if r := a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: "*", Subject: "for everyone"}); r.Error != "" {
		t.Fatalf("broadcast: %+v", r)
	}
	e := lastEventOn(t, remote, a.self())
	if e.Type != bus.Boxed || e.Box == nil || e.Box.Keys[old] != "" || e.Box.Keys[a.self()] == "" {
		t.Fatalf("broadcast as written: %+v", e)
	}
	// What the older device makes of it: an event of a type it doesn't know, passed over; and a's
	// presence, a device with no sessions.
	if e.Clean(); e.Valid() {
		t.Fatal("an older reader would take the box for an event")
	}
	if p := presenceOn(t, remote, a.self()); p.Device != a.self() || len(p.Sessions) != 0 || p.BoxKey == "" || p.Box == nil {
		t.Fatalf("a's presence as an older reader sees it: %+v", p)
	}
}

// syncWriter is a log a test can read while the daemon writes it.
type syncWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// A teammate with push access swaps a device's keys: a presence sealed with another key, or not
// sealed at all, is refused once the device's own key is pinned, and messages go on being encrypted
// to the device's own key.
func TestKeySwapIsRefused(t *testing.T) {
	remote := newRemote(t)
	logs := &syncWriter{}
	dir := filepath.Join(t.TempDir(), "clone")
	git(t, filepath.Dir(dir), "clone", "-q", "--no-local", remote, dir)
	a := &device{t: t, gitDir: filepath.Join(dir, ".git"), stale: 3 * time.Second, poll: 100 * time.Millisecond, logTo: logs}
	a.start()
	b := newDevice(t, remote)
	bob, _ := b.session("bbbbbb", "")
	bdev := b.self()
	eventually(t, "a sees bob", func() bool { return a.seesSession(bob) })
	realKey := presenceOn(t, remote, bdev).BoxKey

	// b goes away, saying goodbye, and its presence is gone; a forger puts its own in its place.
	b.stop()
	eventually(t, "b gone on a", func() bool { return !a.seesSession(bob) })
	evilBox, evilSign := bus.NewBoxKey(), bus.NewSignKey()
	forged := bus.Presence{V: bus.Version, Device: bdev, Host: "forged", At: time.Now().UTC(), BoxKey: bus.BoxPublic(evilBox),
		Sessions: []bus.SessionInfo{{ID: "e0e0e0", Agent: "claude", Started: time.Now().UTC()}}}
	enc, err := bus.EncryptPresence(forged, map[string]string{a.self(): presenceOn(t, remote, a.self()).BoxKey})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := bus.EncodeSealed(enc, bus.PresenceSubject, evilSign, bdev, "refs/murmur/presence/"+bdev)
	if err != nil {
		t.Fatal(err)
	}
	pushMessage(t, remote, "refs/murmur/presence/"+bdev, msg)
	eventually(t, "the refusal", func() bool { return strings.Contains(logs.String(), "isn't the one pinned for "+bdev) })
	if a.seesSession(bdev + "/e0e0e0") {
		t.Fatal("a took the forged presence")
	}
	// Unsealed, the same.
	plain, _ := bus.Encode(bus.Presence{V: bus.Version, Device: bdev, Host: "forged", At: time.Now().UTC(),
		Sessions: []bus.SessionInfo{{ID: "e1e1e1", Agent: "claude", Started: time.Now().UTC()}}}, "x")
	pushMessage(t, remote, "refs/murmur/presence/"+bdev, plain)
	// Refusals are logged once a minute for each device, so this one is known by a having fetched it.
	want := git(t, remote, "rev-parse", "refs/murmur/presence/"+bdev)
	eventually(t, "a fetches the second forgery", func() bool {
		return gitOK(filepath.Dir(a.gitDir), "rev-parse", "refs/murmur/presence/"+bdev) == want
	})
	holds(t, "no forged session on a", 2*time.Second, func() bool {
		return !a.seesSession(bdev+"/e1e1e1") && !a.seesSession(bdev+"/e0e0e0")
	})

	// b comes back with its own keys, and a reads it again; a message to it is encrypted to b's
	// own key, never the forger's.
	b.start()
	b.session("bbbbbb", "")
	eventually(t, "a sees bob again", func() bool { return a.seesSession(bob) })
	if r := a.do(proto.Request{Op: proto.OpSend, To: bob, Subject: "still yours"}); r.Error != "" {
		t.Fatalf("send: %+v", r)
	}
	if got := presenceOn(t, remote, bdev).BoxKey; got != realKey {
		t.Fatalf("b's key changed across a restart: %s, was %s", got, realKey)
	}
	eventually(t, "bob reads it", func() bool {
		in := b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages
		return len(in) == 1 && in[0].Subject == "still yours"
	})
}

// A device whose daemon can no longer push, as when its user is removed from the repo, goes stale
// and is left out of what is written from then on, but for a message addressed to it.
func TestStaleDeviceIsLeftOut(t *testing.T) {
	remote := newRemote(t)
	a, b, c := newDevice(t, remote), newDevice(t, remote), newDevice(t, remote)
	cdev := c.self()
	eventually(t, "all see all", func() bool { return a.seesDevices(3) && b.seesDevices(3) && c.seesDevices(3) })
	a.do(proto.Request{Op: proto.OpSend, To: "*", Subject: "while c is here"})
	if e := lastEventOn(t, remote, a.self()); e.Box == nil || e.Box.Keys[cdev] == "" || e.Box.Keys[b.self()] == "" {
		t.Fatalf("c or b left out while here: %+v", e.Box)
	}

	c.offline(remote, true)
	eventually(t, "c stale on a", func() bool { return !a.fresh(cdev) })
	a.do(proto.Request{Op: proto.OpSend, To: "*", Subject: "after c went"})
	if e := lastEventOn(t, remote, a.self()); e.Box == nil || e.Box.Keys[cdev] != "" || e.Box.Keys[b.self()] == "" {
		t.Fatalf("broadcast after c went: %+v", e.Box)
	}
	eventually(t, "a's presence no longer for c", func() bool {
		p := presenceOn(t, remote, a.self())
		return p.Box != nil && p.Box.Keys[cdev] == "" && p.Box.Keys[b.self()] != ""
	})
	// Addressed to it, within StoppedInbox of when it was last seen, it is still for c.
	if r := a.do(proto.Request{Op: proto.OpSend, To: cdev, Subject: "for c's kept inbox"}); r.Error != "" {
		t.Fatalf("send to c: %+v", r)
	}
	if e := lastEventOn(t, remote, a.self()); e.Box == nil || e.Box.Keys[cdev] == "" {
		t.Fatalf("message to c: %+v", e.Box)
	}
}

// A clone whose keys are lost while its device ID stays takes a new ID: its teammates would refuse
// a new key under the old one.
func TestLostKeysTakeANewDevice(t *testing.T) {
	remote := newRemote(t)
	a := newDevice(t, remote)
	first := a.self()
	a.stop()
	stateDir := filepath.Join(a.gitDir, "murmur")
	if _, err := os.Stat(filepath.Join(stateDir, "keys")); err != nil {
		t.Fatalf("no keys file: %v", err)
	}
	// Restarted with its keys, it is the same device.
	a.start()
	if a.self() != first {
		t.Fatal("a new ID with the same keys")
	}
	a.stop()
	if err := os.Remove(filepath.Join(stateDir, "keys")); err != nil {
		t.Fatal(err)
	}
	a.start()
	if second := a.self(); second == first {
		t.Fatalf("still %s with new keys", first)
	}
	var st struct {
		Pins map[string]json.RawMessage `json:"pins"`
	}
	b, err := os.ReadFile(filepath.Join(stateDir, "state.json"))
	if err != nil || json.Unmarshal(b, &st) != nil || st.Pins[a.self()] == nil {
		t.Fatalf("no pin for the new device: %s %v", b, err)
	}
}

// gitOK runs git and returns its output, or "" if it fails.
func gitOK(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
