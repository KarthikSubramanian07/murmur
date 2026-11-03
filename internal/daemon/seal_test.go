package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/proto"
)

// pushOnto writes a commit with the message make gives for the remote's current tip of ref, on top
// of that tip, from outside Murmur: what anyone who can push could write as another device.
func pushOnto(t *testing.T, remote, ref string, make func(parent string) string) {
	t.Helper()
	parent := ""
	if out := git(t, t.TempDir(), "ls-remote", remote, ref); out != "" {
		parent, _, _ = strings.Cut(out, "\t")
	}
	pushMessage(t, remote, ref, make(parent))
}

// Once a device has sealed what it writes, nothing claiming to be from it is read unless sealed
// with its key: not a record with no seal, nor one sealed with another key, nor its own seal copied
// from another device's log (#155). Its own messages still arrive.
func TestForgedRecordsOfASealingDeviceAreIgnored(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.session("bbbbbb", "")
	eventually(t, "presence", func() bool { return a.seesSession(bob) && b.seesSession(alice) })
	b.do(proto.Request{Op: proto.OpSend, To: alice, Subject: "first, sealed"})
	eventually(t, "the first message", func() bool { return logHas(a, "first, sealed") })

	dev := b.self()
	ref := brand.MeshPrefix + dev
	forged := func(subject string) bus.Event {
		return bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message, From: bob, At: time.Now().UTC(),
			To: alice, Subject: subject}
	}
	pushEvent(t, remote, ref, forged("unsealed forgery"))
	pushOnto(t, remote, ref, func(parent string) string {
		e := forged("sealed with another key")
		msg, err := bus.EncodeSigned(e, e.Line(), bus.NewKey(), dev, ref, parent)
		if err != nil {
			t.Fatal(err)
		}
		return msg
	})
	// A seal made for another ref, put on b's.
	pushOnto(t, remote, ref, func(parent string) string {
		e := forged("sealed for another ref")
		msg, err := bus.EncodeSigned(e, e.Line(), bus.NewKey(), dev, brand.MeshPrefix+"other-0001", parent)
		if err != nil {
			t.Fatal(err)
		}
		return msg
	})
	b.do(proto.Request{Op: proto.OpSend, To: alice, Subject: "second, sealed"})
	eventually(t, "the second message", func() bool { return logHas(a, "second, sealed") })

	var got []string
	for _, m := range a.do(proto.Request{Op: proto.OpInbox, Session: "aaaaaa"}).Messages {
		got = append(got, m.Subject)
	}
	if strings.Join(got, ",") != "first, sealed,second, sealed" {
		t.Fatalf("inbox: %q", got)
	}
}

// A presence pushed over a sealing device's, unsealed, isn't taken for the device's: who keeps
// showing what the device itself last said.
func TestForgedPresenceOfASealingDeviceIsIgnored(t *testing.T) {
	remote := newRemote(t)
	a := newDevice(t, remote)
	b := newDevice(t, remote)
	b.beat = time.Hour // so the device doesn't write over the forgery before a reads it
	b.stop()
	b.start()
	bob, _ := b.session("bbbbbb", "")
	eventually(t, "presence", func() bool { return a.seesSession(bob) })

	dev := b.self()
	fake := bus.Addr(dev, "ffffff")
	msg, err := bus.Encode(bus.Presence{V: bus.Version, Device: dev, Host: "h", At: time.Now().UTC(),
		Sessions: []bus.SessionInfo{{ID: "ffffff", Agent: "test", Started: time.Now().UTC()}}}, "forged")
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	git(t, work, "init", "-q")
	sha := gitIn(t, work, msg, "commit-tree", git(t, work, "hash-object", "-t", "tree", "--stdin"))
	git(t, work, "push", "-q", "--force", remote, sha+":"+brand.PresencePrefix+dev)

	// a reads presence every poll; give it time to have read this one.
	eventually(t, "a to list the forged presence", func() bool {
		return strings.Contains(git(t, a.gitDir, "ls-remote", remote, brand.PresencePrefix+dev), sha)
	})
	time.Sleep(2 * time.Second)
	if a.seesSession(fake) {
		t.Fatal("a forged presence was taken for the device's")
	}
	if !a.seesSession(bob) {
		t.Fatal("the device's own session is gone")
	}
}

// A daemon from before seals writes nothing sealed, and its devices are read as they always were.
func TestUnsealedDeviceIsStillRead(t *testing.T) {
	remote := newRemote(t)
	a := newDevice(t, remote)
	alice, _ := a.session("aaaaaa", "")
	pushEvent(t, remote, brand.MeshPrefix+"old-0001", bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message,
		From: "old-0001/x", At: time.Now().UTC(), To: alice, Subject: "from an older daemon"})
	eventually(t, "the older daemon's message", func() bool { return logHas(a, "from an older daemon") })
}

// A device whose key is lost after it has sealed records becomes a new device, since its peers
// would refuse the new key under its old ID; one that never had a key keeps its ID.
func TestLostKeyMeansANewDevice(t *testing.T) {
	remote := newRemote(t)
	a := newDevice(t, remote)
	before := a.self()
	a.stop()
	state := filepath.Join(a.gitDir, brand.StateDir)
	if err := os.Remove(filepath.Join(state, "key")); err != nil {
		t.Fatal(err)
	}
	a.start()
	if after := a.self(); after == before {
		t.Fatalf("still device %s with a new key", after)
	}

	b := newDevice(t, remote)
	dev := b.self()
	b.stop()
	bstate := filepath.Join(b.gitDir, brand.StateDir)
	for _, f := range []string{"key", "state.json"} {
		if err := os.Remove(filepath.Join(bstate, f)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	b.start()
	if after := b.self(); after != dev {
		t.Fatalf("a device that had no key record became %s, was %s", after, dev)
	}
}
