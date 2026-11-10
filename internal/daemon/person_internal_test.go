package daemon

import (
	"testing"

	"github.com/a-Fig/murmur/internal/bus"
)

// A person's name goes to the first device seen with it, as a session's does, and only a claim in
// the registry gives it to another. Any device can say its person has any name, in its presence or
// on a message, so one that says someone else's is passed over, and the name is marked as seen on
// more than one device.
func TestTheFirstDeviceSeenWithAPersonsNameKeepsIt(t *testing.T) {
	d, _ := namedDaemon(t)
	d.mu.Lock()
	defer d.mu.Unlock()
	tyler, evil := "zz-tyler/"+bus.PersonSession, "aa-evil/"+bus.PersonSession
	person := func(name string) *peer {
		return &peer{pres: bus.Presence{Sessions: []bus.SessionInfo{{ID: bus.PersonSession, Name: name}}}}
	}

	if !d.notePersonLocked("tyler", tyler) || d.st.PeopleOnMany["tyler"] {
		t.Fatalf("the first sighting: changed nothing, or marked the name")
	}
	if !d.notePersonLocked("tyler", evil) {
		t.Errorf("a second device with the name changed nothing")
	}
	if d.st.People["tyler"] != tyler || !d.st.PeopleOnMany["tyler"] {
		t.Errorf("after a second device said tyler: %q, on many %v", d.st.People["tyler"], d.st.PeopleOnMany["tyler"])
	}
	if d.notePersonLocked("tyler", evil) || d.notePersonLocked("tyler", tyler) {
		t.Errorf("a sighting already made changed something")
	}

	// Both presences say tyler, and aa-evil sorts first: the one seen first is still who tyler is.
	d.peers["aa-evil"], d.peers["zz-tyler"] = person("tyler"), person("tyler")
	if addr, ok := d.resolvePersonLocked("tyler"); !ok || addr != tyler {
		t.Errorf("tyler resolves to %q", addr)
	}

	// A message from aa-evil saying it is tyler isn't shown as tyler's, and one from tyler says which
	// device it is from.
	if name, _ := d.personSenderLocked(bus.Event{From: evil, Person: "tyler"}); name != "" {
		t.Errorf("a message from %s is shown from %q", evil, name)
	}
	if name, onMany := d.personSenderLocked(bus.Event{From: tyler, Person: "tyler"}); name != "tyler" || !onMany {
		t.Errorf("tyler's own message: %q, on many %v", name, onMany)
	}
	// A message carrying no name, or a name nobody else has, is shown by what it says or its presence.
	d.peers["aa-evil"] = person("eve")
	d.notePersonLocked("eve", evil)
	if name, onMany := d.personSenderLocked(bus.Event{From: evil}); name != "eve" || onMany {
		t.Errorf("eve, from her presence: %q, on many %v", name, onMany)
	}
	if name, _ := d.personSenderLocked(bus.Event{From: evil, Person: "tyler"}); name != "eve" {
		t.Errorf("eve, saying she is tyler: %q, want her presence's name", name)
	}

	// A claim in the registry stands over every sighting, and a name seen elsewhere first is marked.
	d.notePersonLocked("ada", evil)
	if !d.notePersonClaimLocked("ada", tyler) {
		t.Fatal("the claim changed nothing")
	}
	if addr, _ := d.resolvePersonLocked("ada"); addr != tyler || !d.st.PeopleOnMany["ada"] {
		t.Errorf("ada after a claim: %q, on many %v", addr, d.st.PeopleOnMany["ada"])
	}
	if d.notePersonLocked("ada", evil); d.st.People["ada"] != tyler {
		t.Errorf("a sighting after the claim moved ada to %q", d.st.People["ada"])
	}
}
