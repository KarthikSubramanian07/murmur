package daemon_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
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
	if got, want := view.Muted(r), "You have muted "+aliceName+". Nothing it sends will reach you, until you unmute it.\n"; got != want {
		t.Errorf("what bob is told:\n%s", got)
	}
	if got := view.Muted(b.mute("bbbbbb", "", false)); got != "You have muted: "+aliceName+"\n" {
		t.Errorf("what bob has muted, when he asks:\n%s", got)
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
	// What alice now sends to everyone, or to bob's device, goes out, and she is told bob didn't
	// get it.
	for _, to := range []string{"*", b.self()} {
		if r := a.send("aaaaaa", to, "to many, again", ""); r.Note != bobName+" did not get this message: they have muted you." {
			t.Errorf("a send to %s says %q", to, r.Note)
		}
	}
	// A group is the same: bob is in it, the message goes out, and alice is told he didn't get it.
	b.tagSelf("bbbbbb", []string{"group:ui"}, nil)
	eventually(t, "bob in the group, as alice's device sees it", func() bool { return a.inGroup("aaaaaa", bob, "group:ui") })
	if r := a.send("aaaaaa", "group:ui", "to the group", ""); !strings.Contains(r.Note, bobName+" did not get this message: they have muted you.") {
		t.Errorf("a send to the group says %q", r.Note)
	}
	if r := b.send("dddddd", "*", "from dave to everyone", ""); r.Note != "" {
		t.Errorf("dave, whom nobody muted, is told %q", r.Note)
	}
	// Others still reach bob, and alice still reaches others.
	b.send("dddddd", bobName, "from dave", "")
	if got := inboxOf(b, "bbbbbb"); !slices.Equal(got, []string{"from dave to everyone", "from dave"}) {
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

// A session that reconnects after its daemon restarted is given what arrived while the daemon
// was down, and not what a session it has muted sent (#482): the catch-up left the mute out.
func TestAMuteHoldsWhenASessionReconnects(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.session("bbbbbb", "")
	carol, _ := b.session("cccccc", "")
	dave, _ := b.session("dddddd", "")
	aliceName := b.named(alice)
	b.named(bob)
	b.named(carol)
	b.named(dave)
	if r := b.mute("bbbbbb", aliceName, false); r.Error != "" {
		t.Fatalf("bob mutes alice: %+v", r)
	}

	b.stop()
	a.send("aaaaaa", "*", "while b was down", "")
	b.start()
	// The order makes it the catch-up that decides for bob, every run. Dave, back first, has the
	// message only once the daemon has read it, so bob, back after that, isn't given it as it is
	// read, which checked mutes already. Carol, back last, can only be given it by a catch-up, so
	// the daemon was still catching sessions up when bob came back.
	b.helloAgain("dddddd", true)
	if got := inboxOf(b, "dddddd"); !slices.Contains(got, "while b was down") {
		t.Fatalf("dave, who muted nobody, got %q", got)
	}
	b.helloAgain("bbbbbb", true)
	b.helloAgain("cccccc", true)
	if got := inboxOf(b, "cccccc"); !slices.Contains(got, "while b was down") {
		t.Fatalf("carol, who muted nobody and came back after bob, got %q", got)
	}
	if got := b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages; len(got) != 0 {
		t.Errorf("bob was caught up with a message from the session he muted: %+v", got)
	}
	if got := b.mute("bbbbbb", "", false).Muted; !slices.Equal(got, []string{aliceName}) {
		t.Errorf("after the restart bob has muted %v", got)
	}
}

// A session that another has muted can't add it to a group (#483): the adder is refused once its
// daemon has read of the mute, and an add that goes out anyway isn't applied by the muter's device.
func TestAMutedSessionCantAddItsMuterToAGroup(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.session("bbbbbb", "")
	aliceName, bobName := b.named(alice), a.named(bob)
	b.named(bob)
	eventually(t, "presence", func() bool { return a.seesSession(bob) && b.seesSession(alice) })
	add := func() proto.Response {
		return a.do(proto.Request{Op: proto.OpTags, Session: "aaaaaa", Add: []string{"group:ui"}, For: []string{bobName}})
	}

	if r := b.mute("bbbbbb", aliceName, false); r.Error != "" {
		t.Fatalf("bob mutes alice: %+v", r)
	}
	// Until alice's daemon reads of the mute, her add goes out, and bob's device leaves it. An add
	// can be refused for another reason meanwhile, as when the tests' load holds b's heartbeat back
	// and a takes its sessions for gone (#387), so the wait is for this refusal.
	eventually(t, "alice's add to be refused for the mute", func() bool {
		return strings.Contains(add().Error, bobName+" has muted you")
	})
	holds(t, "bob out of the group", 2*time.Second, func() bool { return !b.inGroup("bbbbbb", bob, "group:ui") })

	if r := b.mute("bbbbbb", aliceName, true); r.Error != "" {
		t.Fatalf("bob unmutes alice: %+v", r)
	}
	eventually(t, "alice's add to go through", func() bool { return add().Error == "" })
	eventually(t, "bob in the group", func() bool { return b.inGroup("bbbbbb", bob, "group:ui") })
}

// toldOf reads session sess's inbox until it holds n notices from Murmur, and returns them. Anything
// else in the inbox fails the test.
func (d *device) toldOf(sess string, n int) []proto.MessageView {
	d.t.Helper()
	var told []proto.MessageView
	eventually(d.t, fmt.Sprintf("%d notice(s) from %s", n, brand.Product), func() bool {
		for _, m := range d.do(proto.Request{Op: proto.OpInbox, Session: sess}).Messages {
			if !m.Receipt {
				d.t.Fatalf("%s's inbox holds a message that isn't a notice: %+v", sess, m)
			}
			told = append(told, m)
		}
		return len(told) >= n
	})
	return told
}

// A direct message from a session whose daemon hadn't read of the mute, sent after the grace, is
// dropped by the muting device, and its sender is told so once, by Murmur, about that message,
// without a turn (#466). Here the muting device can't reach the remote while its session mutes, so
// its presence with the mute can't be read by anyone until the message has gone out.
func TestAMutedSenderIsToldItsDirectMessageWasDropped(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	exe, calls := useFakeT3(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	alice, _ := a.sessionRoute("aaaaaa", "claude-ada", proto.Route{T3CLI: exe, T3Thread: "thread-ada"})
	bob, _ := b.session("bbbbbb", "")
	aliceName, bobName := b.named(alice), a.named(bob)
	eventually(t, "presence", func() bool { return a.seesSession(bob) && a.nameOf(bob) == bobName })

	b.offline(remote, true)
	if r := b.mute("bbbbbb", aliceName, false); r.Error != "" {
		t.Fatalf("bob mutes alice: %+v", r)
	}
	r := a.send("aaaaaa", bobName, "Take #12 over?", "Branch fix-12.")
	b.offline(remote, false)

	told := a.toldOf("aaaaaa", 1)[0]
	t.Logf("send's result:\n%s\nthe inbox:\n%s\nthe hook's notice:\n%s", view.Sent(r),
		view.Inbox(proto.Response{Messages: []proto.MessageView{told}}), view.Notices([]proto.MessageView{told}, 1))
	if want := bobName + ` did not get your message "Take #12 over?": they have muted you`; told.Subject != want {
		t.Errorf("the notice's subject is %q, want %q", told.Subject, want)
	}
	if want := "Nothing you send can reach them until they unmute you."; told.Body != want {
		t.Errorf("the notice's body is %q, want %q", told.Body, want)
	}
	if told.FromName != brand.Product || told.FromOwner != "" {
		t.Errorf("the notice is from %q (%q), want %s", told.FromName, told.FromOwner, brand.Product)
	}
	if got := b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages; len(got) != 0 {
		t.Errorf("bob got a message from the session he muted: %+v", got)
	}
	if log := git(t, remote, "log", "--format=%s", "refs/murmur/mesh/"+a.self()); strings.Contains(log, told.ID) {
		t.Errorf("the notice went on the bus:\n%s", log)
	}
	// Once, and nobody is woken: not the sender's thread, which the notice waits for.
	holds(t, "no second notice", 2*time.Second, func() bool {
		return len(a.do(proto.Request{Op: proto.OpInbox, Session: "aaaaaa"}).Messages) == 0
	})
	for _, c := range calls() {
		if slices.Contains(c.Args, "thread-ada") {
			t.Errorf("the notice was handed to the sender's thread as a turn: %+v", c)
		}
	}
}

// A message to everyone or to a device, sent before the sender's daemon had read of the mute, is
// dropped for the session that muted it from the first moment, and send's result can't name it.
// The sender is told by a notice for each message (#466). Once its daemon has read the mute, send
// names the muter, and the drop's mark tells it nothing more.
func TestAMutedSenderIsToldItsMessageToManyWasDropped(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.session("bbbbbb", "")
	dave, _ := b.session("dddddd", "")
	aliceName, bobName := b.named(alice), a.named(bob)
	a.named(dave)
	b.named(dave)
	eventually(t, "presence", func() bool { return a.seesSession(bob) && a.seesSession(dave) })

	b.offline(remote, true)
	if r := b.mute("bbbbbb", aliceName, false); r.Error != "" {
		t.Fatalf("bob mutes alice: %+v", r)
	}
	for _, to := range []string{"*", b.self()} {
		if r := a.send("aaaaaa", to, "to "+to, ""); strings.Contains(r.Note, "muted") {
			t.Errorf("a send to %s, before alice's daemon could know of the mute, says %q", to, r.Note)
		}
	}
	b.offline(remote, false)

	told := a.toldOf("aaaaaa", 2)
	t.Logf("the inbox:\n%s", view.Inbox(proto.Response{Messages: told}))
	var subjects []string
	for _, m := range told {
		subjects = append(subjects, m.Subject)
	}
	slices.Sort(subjects)
	want := []string{
		bobName + ` did not get your message "to *": they have muted you`,
		bobName + ` did not get your message "to ` + b.self() + `": they have muted you`,
	}
	slices.Sort(want)
	if !slices.Equal(subjects, want) {
		t.Errorf("the notices say %q, want %q", subjects, want)
	}
	if got := inboxWant(b, "dddddd", 2); len(got) != 2 {
		t.Errorf("dave, who muted nobody, has %v", got)
	}

	// Now alice's daemon knows: send says bob didn't get it, and that is all she is told.
	eventually(t, "alice's daemon to read the mute", func() bool {
		l := a.do(proto.Request{Op: proto.OpLookup, Session: "aaaaaa", Name: bobName}).Lookup
		return l != nil && slices.Contains(l.Muted, aliceName)
	})
	if r := a.send("aaaaaa", "*", "to everyone, again", ""); r.Note != bobName+" did not get this message: they have muted you." {
		t.Errorf("a send to everyone says %q", r.Note)
	}
	holds(t, "no notice of a mute send named", 3*time.Second, func() bool {
		return len(a.do(proto.Request{Op: proto.OpInbox, Session: "aaaaaa"}).Messages) == 0
	})
}
