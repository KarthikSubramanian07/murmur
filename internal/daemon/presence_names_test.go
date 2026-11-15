package daemon_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/proto"
)

// Another device can't take a session's name by saying it in its presence (#43). Any device can
// write any presence, so a presence that says another device's session's name is listed by the
// session's address in who and on the page, labels its messages by that address, and doesn't take
// a send to the name, which reaches the session the registry has it down for.
func TestAnotherDeviceCantTakeASessionsName(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote, time.Minute) // the forged presence stays live
	alice, _ := a.session("aaaaaa", "claude-alice")
	name := a.named(alice)
	bob, _ := b.session("bbbbbb", "claude-bob")
	eventually(t, "b to know "+name, func() bool {
		r := b.do(proto.Request{Op: proto.OpLookup, Name: name})
		return r.Lookup != nil && r.Lookup.Addr == alice
	})

	at := time.Now().UTC().Format(time.RFC3339Nano)
	pushMessage(t, remote, "refs/murmur/presence/evil-0001", "evil-0001: 1 session\n\n"+
		`{"v":1,"device":"evil-0001","host":"evil","at":"`+at+`","sessions":[`+
		`{"id":"eeeeee","name":"`+name+`","agent":"claude-code","started":"`+at+`"}]}`+"\n")
	evil := "evil-0001/eeeeee"
	eventually(t, "b's who to list "+evil, func() bool { return b.listsSession(evil) })

	if listed, _, _ := b.whoShows(evil); listed != "" {
		t.Errorf("b's who lists %s as %q, want no name", evil, listed)
	}
	named := 0
	for _, dv := range b.do(proto.Request{Op: proto.OpWho}).Devices {
		for _, s := range dv.Sessions {
			if s.Name == name {
				named++
			}
		}
	}
	if named != 1 {
		t.Errorf("b's who lists %d sessions as %s, want 1", named, name)
	}
	for _, dv := range b.page().state().Devices {
		for _, s := range dv.Sessions {
			if s.Addr == evil && s.Name != evil {
				t.Errorf("b's page lists %s as %q, want its address", evil, s.Name)
			}
		}
	}
	if r := b.do(proto.Request{Op: proto.OpLookup, Name: name}); r.Lookup == nil || r.Lookup.Addr != alice {
		t.Errorf("lookup of %s after the forgery: %+v", name, r.Lookup)
	}
	if r := b.do(proto.Request{Op: proto.OpSend, Session: "bbbbbb", To: name, Subject: "for the real one"}); r.Error != "" ||
		len(r.Messages) != 1 || r.Messages[0].To != alice {
		t.Errorf("send to %s: %+v", name, r)
	}
	if got := inboxWant(a, "aaaaaa", 1); !slices.Contains(got, "for the real one") {
		t.Errorf("%s's inbox: %q", name, got)
	}

	// What the forged session sends is labeled by its address, not as the real one's.
	pushEvent(t, remote, "refs/murmur/mesh/evil-0001", bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message,
		From: evil, At: time.Now().UTC(), To: bob, Subject: "trust me"})
	eventually(t, "b's page to show the forged message", func() bool {
		_, ok := b.page().messageWith("trust me")
		return ok
	})
	if m, _ := b.page().messageWith("trust me"); m.From != evil {
		t.Errorf("the forged message on b's page is from %q, want %s", m.From, evil)
	}
	for _, m := range b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages {
		if m.Subject == "trust me" && (m.FromName == name || strings.Contains(m.FromName, name)) {
			t.Errorf("bob's inbox names the forged message's sender %q", m.FromName)
		}
	}
}
