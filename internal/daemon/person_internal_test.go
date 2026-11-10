package daemon

import (
	"testing"

	"github.com/a-Fig/murmur/internal/bus"
)

// A person's name goes to the first device whose presence is seen with it, as a session's does,
// and only a claim in the registry gives it to another. Any device can say its person has any
// name, in its presence or on a message, so one that says someone else's is passed over. A message
// counts for nothing: its name is shown only if its sender holds the name.
func TestTheFirstDeviceSeenWithAPersonsNameKeepsIt(t *testing.T) {
	d, _ := namedDaemon(t)
	d.mu.Lock()
	defer d.mu.Unlock()
	tyler, evil := "zz-tyler/"+bus.PersonSession, "aa-evil/"+bus.PersonSession
	person := func(name string) *peer {
		return &peer{pres: bus.Presence{Sessions: []bus.SessionInfo{{ID: bus.PersonSession, Name: name}}}}
	}

	if !d.notePersonLocked("tyler", tyler) {
		t.Fatalf("the first sighting changed nothing")
	}
	if d.notePersonLocked("tyler", evil) || d.notePersonLocked("tyler", tyler) {
		t.Errorf("a second device with the name, or a sighting already made, changed something")
	}
	if d.st.People["tyler"] != tyler {
		t.Errorf("after a second device said tyler: %q", d.st.People["tyler"])
	}

	// Both presences say tyler, and aa-evil sorts first: the one seen first is still who tyler is.
	d.peers["aa-evil"], d.peers["zz-tyler"] = person("tyler"), person("tyler")
	if addr, ok := d.resolvePersonLocked("tyler"); !ok || addr != tyler {
		t.Errorf("tyler resolves to %q", addr)
	}

	// A message from aa-evil saying it is tyler isn't shown as tyler's, and one from tyler says which
	// device it is from, while the other presence says tyler too.
	if name, _ := d.personSenderLocked(bus.Event{From: evil, Person: "tyler"}); name != "" {
		t.Errorf("a message from %s is shown from %q", evil, name)
	}
	if name, onMany := d.personSenderLocked(bus.Event{From: tyler, Person: "tyler"}); name != "tyler" || !onMany {
		t.Errorf("tyler's own message: %q, on many %v", name, onMany)
	}

	// Once aa-evil's presence says eve, tyler is on one device again, and eve's messages are hers
	// whatever name they carry. A name only a message says is no one's, and isn't taken.
	d.peers["aa-evil"] = person("eve")
	d.notePersonLocked("eve", evil)
	if name, onMany := d.personSenderLocked(bus.Event{From: tyler, Person: "tyler"}); name != "tyler" || onMany {
		t.Errorf("tyler's message once the other presence went: %q, on many %v", name, onMany)
	}
	for _, carried := range []string{"", "tyler", "grace"} {
		if name, onMany := d.personSenderLocked(bus.Event{From: evil, Person: carried}); name != "eve" || onMany {
			t.Errorf("eve, saying %q: %q, on many %v; want her presence's name", carried, name, onMany)
		}
	}
	if _, ok := d.st.People["grace"]; ok {
		t.Errorf("a name only a message carried was recorded")
	}
	d.namesChecked, d.st.NamesPeople = true, true
	if dev := d.nameTakenLocked("grace"); dev != "" {
		t.Errorf("grace, which only a message carried, is taken by %s", dev)
	}
	if dev := d.nameTakenLocked("eve"); dev != "aa-evil" {
		t.Errorf("eve, in a live presence, is taken by %q", dev)
	}

	// A claim in the registry stands over every sighting.
	d.notePersonLocked("ada", evil)
	if !d.notePersonClaimLocked("ada", tyler) {
		t.Fatal("the claim changed nothing")
	}
	if addr, _ := d.resolvePersonLocked("ada"); addr != tyler {
		t.Errorf("ada after a claim: %q", addr)
	}
	if d.notePersonLocked("ada", evil); d.st.People["ada"] != tyler {
		t.Errorf("a sighting after the claim moved ada to %q", d.st.People["ada"])
	}
	// Once the registry is read, a sighting alone, from a presence that has gone, takes nothing.
	d.notePersonLocked("linus", "gone-0001/"+bus.PersonSession)
	if dev := d.nameTakenLocked("linus"); dev != "" {
		t.Errorf("linus, seen once on a device whose presence has gone, is taken by %s", dev)
	}
	d.namesChecked = false
	if dev := d.nameTakenLocked("linus"); dev != "gone-0001" {
		t.Errorf("before the registry is read, linus is taken by %q, want the device last seen with it", dev)
	}
}

// A person who renamed before #272 has no claim to either name, and once their presence has gone
// only People says the names are theirs. Their message carrying their later name is shown from it,
// not from whichever of their names sorts first.
func TestAGonePersonsMessageKeepsItsName(t *testing.T) {
	d, _ := namedDaemon(t)
	d.mu.Lock()
	defer d.mu.Unlock()
	alice, evil := "zz-alice/"+bus.PersonSession, "aa-evil/"+bus.PersonSession
	d.notePersonLocked("ada", alice)
	d.notePersonLocked("grace", alice)
	if name, _ := d.personSenderLocked(bus.Event{From: alice, Person: "grace"}); name != "grace" {
		t.Errorf("alice's message as grace, her presence gone: %q", name)
	}
	if name, _ := d.personSenderLocked(bus.Event{From: evil, Person: "grace"}); name != "" {
		t.Errorf("another device's message as grace: %q", name)
	}
}
