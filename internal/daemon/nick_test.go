package daemon_test

import (
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/view"
)

// Tests of a device's nickname (#190): its user sets it, teammates see it beside the device's ID,
// and it stands in for the ID in an address.

// nickOf is what device d's who says the device id is called.
func (d *device) nickOf(id string) string {
	for _, dv := range d.do(proto.Request{Op: proto.OpWho}).Devices {
		if dv.ID == id {
			return dv.Nick
		}
	}
	return ""
}

func (d *device) setNick(nick string) proto.Response {
	d.t.Helper()
	r := d.do(proto.Request{Op: proto.OpNick, Nick: nick})
	if r.Error != "" {
		d.t.Fatalf("the nickname %s: %s", nick, r.Error)
	}
	return r
}

func TestADevicesNicknameIsSeenAndIsAnAddress(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	bob, _ := b.session("bbbbbb", "")
	eventually(t, "a to see b's session", func() bool { _, ok := a.whoEntry(bob); return ok })

	if r := b.do(proto.Request{Op: proto.OpNick}); !strings.Contains(r.Note, "has no nickname.") {
		t.Errorf("before one is set: %+v", r)
	}
	if r := b.setNick("tyler"); !strings.Contains(r.Note, "has the nickname tyler") {
		t.Errorf("setting it: %+v", r)
	}
	if got := b.nickOf(b.self()); got != "tyler" {
		t.Errorf("its own who calls it %q", got)
	}
	eventually(t, "a to see b's nickname", func() bool { return a.nickOf(b.self()) == "tyler" })
	if got := view.Who(a.do(proto.Request{Op: proto.OpWho})); !strings.Contains(got, "\ntyler  "+b.self()+" (") {
		t.Errorf("who doesn't show the nickname beside the ID:\n%s", got)
	}

	// The nickname stands in for the device's ID; what is sent, and shown as sent, is the ID.
	r := a.do(proto.Request{Op: proto.OpSend, To: "tyler/bbbbbb", Subject: "by nickname"})
	if r.Error != "" || len(r.Messages) != 1 || r.Messages[0].To != bob {
		t.Fatalf("a send to tyler/bbbbbb: %+v", r)
	}
	eventually(t, "the message to arrive", func() bool { return len(inboxOf(b, "bbbbbb")) == 1 })
	if r := a.do(proto.Request{Op: proto.OpSend, To: "tyler", Subject: "to the device"}); r.Error != "" || r.Messages[0].To != b.self() {
		t.Errorf("a send to the device by nickname: %+v", r)
	}
	l := a.do(proto.Request{Op: proto.OpLookup, Name: "tyler/bbbbbb"})
	if l.Error != "" || l.Lookup.Addr != bob || l.Lookup.Nick != "tyler" {
		t.Fatalf("lookup by nickname: %+v", l)
	}
	if got := view.Lookup(l); !strings.Contains(got, "  device tyler  "+b.self()+" (") {
		t.Errorf("lookup doesn't show the nickname:\n%s", got)
	}

	// Two devices may have one nickname. The session then says which is meant, and an address
	// that could mean either is refused.
	if r := a.setNick("tyler"); !strings.Contains(r.Note, b.self()+" is called tyler too") {
		t.Errorf("taking a nickname another device has: %+v", r)
	}
	if r := a.do(proto.Request{Op: proto.OpSend, To: "tyler/bbbbbb", Subject: "still b's"}); r.Error != "" || r.Messages[0].To != bob {
		t.Errorf("a session only one of the two has: %+v", r)
	}
	r = a.do(proto.Request{Op: proto.OpSend, To: "tyler", Subject: "which?"})
	if !strings.Contains(r.Error, "2 devices are called tyler") || !strings.Contains(r.Error, a.self()) || !strings.Contains(r.Error, b.self()) {
		t.Errorf("a send to a nickname two devices have: %+v", r)
	}

	if r := a.do(proto.Request{Op: proto.OpNick, Clear: true}); r.Error != "" || a.nickOf(a.self()) != "" {
		t.Errorf("taking it off: %+v, and who says %q", r, a.nickOf(a.self()))
	}
	for _, bad := range []string{"Tyler S", "x", b.self()} {
		if r := a.do(proto.Request{Op: proto.OpNick, Nick: bad}); r.Error == "" {
			t.Errorf("the nickname %q was taken: %+v", bad, r)
		}
	}
}

// A device with no nickname is called by its person's name, which nobody else's device can take.
func TestADeviceIsCalledByItsPersonsName(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	alice, _ := a.session("aaaaaa", "")
	a.page().choose("ada-lovelace")
	if got := a.nickOf(a.self()); got != "ada-lovelace" {
		t.Errorf("its own who calls it %q", got)
	}
	eventually(t, "b to call a by its person's name", func() bool {
		_, ok := b.whoEntry(alice)
		return ok && b.nickOf(a.self()) == "ada-lovelace"
	})
	if r := b.do(proto.Request{Op: proto.OpSend, To: "ada-lovelace/aaaaaa", Subject: "hi"}); r.Error != "" || r.Messages[0].To != alice {
		t.Errorf("a send to a session by its device's person: %+v", r)
	}
	if r := b.do(proto.Request{Op: proto.OpNick, Nick: "ada-lovelace"}); !strings.Contains(r.Error, "the name of the person on "+a.self()) {
		t.Errorf("taking another device's person's name: %+v", r)
	}
	// A nickname of its own stands before the person's name.
	a.setNick("ada-desktop")
	eventually(t, "b to see the nickname", func() bool { return b.nickOf(a.self()) == "ada-desktop" })
}
