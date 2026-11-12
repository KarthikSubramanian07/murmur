package daemon_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/view"
)

// Tests of mutes (#27): a session mutes another by name, gets nothing it sends from then on, and
// the muted session learns of it only when it sends to the one that muted it.

func (d *device) mute(id, name string, unmute bool) proto.Response {
	d.t.Helper()
	return d.do(proto.Request{Op: proto.OpMute, Session: id, Name: name, Unmute: unmute})
}

// hasMute says the page lists the mute of muted by the session called by, and whether it offers
// to undo it.
func (p *page) hasMute(by, muted string) (listed, canUnmute bool) {
	for _, m := range p.state().Mutes {
		if m.By == by && m.Muted == muted {
			return true, m.CanUnmute
		}
	}
	return false, false
}

func TestAMutedSessionReachesNobodyWhoMutedIt(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.session("bbbbbb", "")
	dave, _ := b.session("dddddd", "")
	aliceName, bobName := b.named(alice), a.named(bob)
	a.named(dave)
	b.named(bob)

	// What can't be muted: yourself, a name nobody has, a person.
	for _, bad := range []string{bobName, "ada-lovelace", "ada"} {
		if r := b.mute("bbbbbb", bad, false); r.Error == "" {
			t.Errorf("bob muted %q: %+v", bad, r)
		}
	}
	if r := b.mute("bbbbbb", "", false); r.Error != "" || len(r.Muted) != 0 || view.Muted(r) != "You have muted nobody.\n" {
		t.Errorf("before any mute: %+v", r)
	}

	r := b.mute("bbbbbb", aliceName, false)
	if r.Error != "" || !slices.Equal(r.Muted, []string{aliceName}) {
		t.Fatalf("bob mutes alice: %+v", r)
	}
	if got := view.Muted(r); !strings.Contains(got, "Muted "+aliceName) || !strings.Contains(got, "You have muted: "+aliceName) {
		t.Errorf("what bob is told:\n%s", got)
	}

	// To everyone: bob's device drops it for bob and queues it for dave. The device decides, so
	// this holds before alice's daemon has read of the mute.
	a.send("aaaaaa", "*", "to everyone", "")
	if got := inboxOf(b, "dddddd"); !slices.Equal(got, []string{"to everyone"}) {
		t.Errorf("dave, who muted nobody, has %v", got)
	}
	if got := b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages; len(got) != 0 {
		t.Errorf("bob has a message from the session he muted: %+v", got)
	}

	// To bob: once alice's daemon has read bob's presence, it refuses, and that is how alice learns.
	var refusal string
	eventually(t, "alice's send to bob to be refused", func() bool {
		refusal = a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: bobName, Subject: "to bob"}).Error
		return refusal != ""
	})
	if !strings.Contains(refusal, bobName+" has muted you") {
		t.Errorf("the refusal: %s", refusal)
	}
	// Others still reach bob, and alice still reaches others.
	b.send("dddddd", bobName, "from dave", "")
	if got := inboxOf(b, "bbbbbb"); !slices.Equal(got, []string{"from dave"}) {
		t.Errorf("bob has %v, want only dave's: nothing alice sent before the refusal", got)
	}

	// The mute is on the bus: lookup shows it anywhere, and both pages list it. Only bob's own
	// device's page can undo it.
	if l := a.do(proto.Request{Op: proto.OpLookup, Session: "aaaaaa", Name: bobName}); l.Lookup == nil || !slices.Equal(l.Lookup.Muted, []string{aliceName}) ||
		!strings.Contains(view.Lookup(l), "has muted: "+aliceName) {
		t.Errorf("lookup of bob from a: %+v", l.Lookup)
	}
	pa, pb := a.page(), b.page()
	if listed, can := pb.hasMute(bobName, aliceName); !listed || !can {
		t.Errorf("bob's device's page: listed %v, can unmute %v", listed, can)
	}
	if listed, can := pa.hasMute(bobName, aliceName); !listed || can {
		t.Errorf("alice's device's page: listed %v, can unmute %v", listed, can)
	}
	body := `{"by":"` + bobName + `","muted":"` + aliceName + `"}`
	if code, _ := pa.call("POST", "/api/unmute", pa.token, body); code < http.StatusBadRequest {
		t.Error("another device's page undid the mute")
	}
	if code, text := pb.call("POST", "/api/unmute", pb.token, body); code >= http.StatusBadRequest {
		t.Fatalf("bob's device's page undoing the mute: %d %s", code, text)
	}
	if listed, _ := pb.hasMute(bobName, aliceName); listed {
		t.Error("the mute is still listed after it was undone")
	}
	eventually(t, "alice to reach bob again", func() bool {
		return a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: bobName, Subject: "after the unmute"}).Error == ""
	})
	if got := inboxOf(b, "bbbbbb"); !slices.Contains(got, "after the unmute") {
		t.Errorf("after the unmute bob has %v", got)
	}

	// The session undoes its own mute the same way.
	b.mute("bbbbbb", aliceName, false)
	if r := b.mute("bbbbbb", aliceName, true); r.Error != "" || len(r.Muted) != 0 {
		t.Errorf("bob unmutes alice: %+v", r)
	}
	if r := b.mute("bbbbbb", aliceName, true); r.Error == "" {
		t.Errorf("unmuting a session that isn't muted: %+v", r)
	}
}
