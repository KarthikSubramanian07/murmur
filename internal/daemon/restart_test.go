package daemon_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/t4/internal/bus"
	"github.com/a-Fig/t4/internal/proto"
)

// Tests of what survives, and what must not, when a daemon stops and starts again.

func (d *device) restart() {
	d.stop()
	d.start()
}

func (d *device) seesDevices(n int) bool {
	return len(d.do(proto.Request{Op: proto.OpWho}).Devices) == n
}

func (d *device) seesSession(addr string) bool {
	for _, dv := range d.do(proto.Request{Op: proto.OpWho}).Devices {
		for _, s := range dv.Sessions {
			if s.Addr == addr {
				return true
			}
		}
	}
	return false
}

func (d *device) openClaims() []proto.ClaimView {
	return d.do(proto.Request{Op: proto.OpClaims}).Claims
}

func (d *device) helloAgain(id string, reconnect bool) {
	d.t.Helper()
	conn, _, err := d.c.Hello(context.Background(), proto.Hello{ID: id, Agent: "test", Reconnect: reconnect})
	if err != nil {
		d.t.Fatalf("hello %s: %v", id, err)
	}
	d.t.Cleanup(func() { conn.Close() })
}

// holds stays true for a while: for checking that something doesn't come back.
func holds(t *testing.T, what string, d time.Duration, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		if !cond() {
			t.Fatalf("%s stopped holding", what)
		}
	}
}

func TestRestartDoesNotReplayToNewSessions(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	eventually(t, "presence", func() bool { return a.seesDevices(2) && b.seesDevices(2) })

	b.stop()
	a.do(proto.Request{Op: proto.OpSend, To: "*", Subject: "sent while b was down"})
	b.start()
	b.session("cccccc", "") // a new session, registered as soon as the daemon is up
	eventually(t, "b back", func() bool { return b.seesDevices(2) })
	a.do(proto.Request{Op: proto.OpSend, To: "*", Subject: "sent after"})

	for _, sess := range []string{"cccccc", proto.CLISession} {
		var got []string
		eventually(t, "the new message for "+sess, func() bool {
			for _, m := range b.do(proto.Request{Op: proto.OpInbox, Session: sess}).Messages {
				got = append(got, m.Subject)
			}
			return len(got) > 0
		})
		if len(got) != 1 || got[0] != "sent after" {
			t.Fatalf("%s got %q", sess, got)
		}
	}
}

func TestInboxSurvivesCleanRestart(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	bob, _ := b.session("bbbbbb", "")
	eventually(t, "presence", func() bool { return a.seesSession(bob) })
	a.do(proto.Request{Op: proto.OpSend, To: bob, Subject: "unread across a restart"})
	eventually(t, "delivery", func() bool {
		return len(b.do(proto.Request{Op: proto.OpNotices, Session: "bbbbbb"}).Messages) == 1
	})

	b.restart()
	b.helloAgain("bbbbbb", true)
	got := b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages
	if len(got) != 1 || got[0].Subject != "unread across a restart" {
		t.Fatalf("inbox after restart: %+v", got)
	}
}

func TestCLIClaimEndsWithItsDaemon(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	if r := b.do(proto.Request{Op: proto.OpClaim, What: "landing page"}); r.Claimed == nil {
		t.Fatalf("claim: %+v", r)
	}
	eventually(t, "a sees the claim", func() bool { return len(a.openClaims()) == 1 })

	b.restart()
	eventually(t, "b back", func() bool { return a.seesDevices(2) })
	holds(t, "the claim staying ended", 2*time.Second, func() bool { return len(a.openClaims()) == 0 })
	if open := b.openClaims(); len(open) != 0 {
		t.Fatalf("b still shows %+v", open)
	}
	// The human who made it is told, in the CLI's inbox.
	eventually(t, "the notice", func() bool {
		for _, m := range b.do(proto.Request{Op: proto.OpInbox}).Messages {
			if strings.Contains(m.Subject, "has ended") {
				return true
			}
		}
		return false
	})
}

func TestGoodbyeEndsClaimsAtOnce(t *testing.T) {
	remote := newRemote(t)
	// A real stale limit: the goodbye must end the claim long before it would have lapsed.
	a, b := newDevice(t, remote, time.Minute), newDevice(t, remote, time.Minute)
	b.do(proto.Request{Op: proto.OpClaim, What: "billing"})
	eventually(t, "a sees the claim", func() bool { return len(a.openClaims()) == 1 })
	b.stop()
	start := time.Now()
	eventually(t, "the claim ends", func() bool { return len(a.openClaims()) == 0 })
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("took %s", took)
	}
}

// newDeviceWith is newDevice with its own poll interval and stale limit.
func newDeviceWith(t *testing.T, remote string, poll, stale time.Duration) *device {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	git(t, filepath.Dir(dir), "clone", "-q", remote, dir)
	d := &device{t: t, gitDir: filepath.Join(dir, ".git"), stale: stale, poll: poll}
	d.start()
	return d
}

func claimsBy(d *device, addr string) int {
	n := 0
	for _, c := range d.openClaims() {
		if c.By == addr {
			n++
		}
	}
	return n
}

func (d *device) claimAs(sess, what, seen string) proto.Response {
	d.t.Helper()
	return d.do(proto.Request{Op: proto.OpClaim, Session: sess, What: what, Seen: seen})
}

// told waits for a message from t4 itself in a session's inbox, containing text.
func (d *device) told(sess, text string) {
	d.t.Helper()
	eventually(d.t, "a notice containing "+text, func() bool {
		for _, m := range d.do(proto.Request{Op: proto.OpInbox, Session: sess}).Messages {
			if strings.Contains(m.Subject, text) {
				return true
			}
		}
		return false
	})
}

func (d *device) offline(remote string, off bool) {
	url := remote
	if off {
		url = filepath.Join(filepath.Dir(remote), "nowhere.git")
	}
	git(d.t, filepath.Dir(d.gitDir), "config", "remote.origin.url", url)
}

// A claim made past a device that said goodbye ends that device's claims for good: when it
// restarts and its session reconnects, the old claim stays ended, on every device, and the
// holder is told.
func TestClaimPastAGoneDeviceEndsItsClaims(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote, time.Minute), newDevice(t, remote, time.Minute)
	bob, _ := b.session("bbbbbb", "")
	if r := b.claimAs("bbbbbb", "search", ""); r.Claimed == nil {
		t.Fatalf("claim: %+v", r)
	}
	eventually(t, "a sees bob's claim", func() bool { return claimsBy(a, bob) == 1 })

	b.stop()
	eventually(t, "the goodbye ends it on a", func() bool { return claimsBy(a, bob) == 0 })
	a.session("aaaaaa", "")
	tok := a.do(proto.Request{Op: proto.OpClaims, Session: "aaaaaa"}).Seen
	if r := a.claimAs("aaaaaa", "search", tok); r.Claimed == nil {
		t.Fatalf("a's claim: %+v", r)
	}

	b.start()
	b.helloAgain("bbbbbb", true)
	eventually(t, "a sees bob again", func() bool { return a.seesSession(bob) })
	holds(t, "bob's old claim staying ended", 2*time.Second, func() bool { return claimsBy(a, bob) == 0 })
	if n := claimsBy(b, bob); n != 0 {
		t.Fatalf("b still counts bob's ended claim (%d)", n)
	}
	b.told("bbbbbb", "was ended by")
}

// A lapse nobody claimed past is undone: the claim holds again when its device is back.
func TestUncontestedLapseIsUndone(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote, time.Minute), newDevice(t, remote, time.Minute)
	bob, _ := b.session("bbbbbb", "")
	b.claimAs("bbbbbb", "search", "")
	eventually(t, "a sees bob's claim", func() bool { return claimsBy(a, bob) == 1 })
	b.stop()
	eventually(t, "the goodbye ends it on a", func() bool { return claimsBy(a, bob) == 0 })
	b.start()
	b.helloAgain("bbbbbb", true)
	eventually(t, "the claim back", func() bool { return claimsBy(a, bob) == 1 })
}

// A device out of touch long enough looks gone. A claim made past it then ends its claims for
// good, and once it is back its session is told.
func TestClaimPastAnOutOfTouchDevice(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	bob, _ := b.session("bbbbbb", "")
	b.claimAs("bbbbbb", "search", "")
	eventually(t, "a sees bob's claim", func() bool { return claimsBy(a, bob) == 1 })

	b.offline(remote, true)
	eventually(t, "the claim lapses on a", func() bool { return claimsBy(a, bob) == 0 })
	a.session("aaaaaa", "")
	tok := a.do(proto.Request{Op: proto.OpClaims, Session: "aaaaaa"}).Seen
	if r := a.claimAs("aaaaaa", "search", tok); r.Claimed == nil {
		t.Fatalf("a's claim: %+v", r)
	}
	b.offline(remote, false)

	eventually(t, "a sees bob again", func() bool { return a.seesSession(bob) })
	holds(t, "bob's claim staying ended", 2*time.Second, func() bool { return claimsBy(a, bob) == 0 })
	b.told("bbbbbb", "was ended by")
}

// A daemon back from an outage judges from fresh presence before it claims, so it doesn't take
// a live device for gone and end its claims.
func TestReaderBackFromOutageSeesLiveClaims(t *testing.T) {
	remote := newRemote(t)
	a := newDeviceWith(t, remote, 5*time.Second, 10*time.Second)
	b := newDevice(t, remote, 10*time.Second)
	bob, _ := b.session("bbbbbb", "")
	b.claimAs("bbbbbb", "search", "")
	a.session("aaaaaa", "")
	eventually(t, "a sees bob's claim", func() bool { return claimsBy(a, bob) == 1 })

	a.offline(remote, true)
	time.Sleep(16 * time.Second) // past the stale limit; b keeps heartbeating meanwhile
	a.offline(remote, false)

	r := a.do(proto.Request{Op: proto.OpClaims, Session: "aaaaaa"})
	if len(r.Claims) != 1 {
		t.Fatalf("a was shown %d claims, want bob's", len(r.Claims))
	}
	if c := a.claimAs("aaaaaa", "search too", r.Seen); c.Claimed == nil {
		t.Fatalf("a's claim, made with bob's in view: %+v", c)
	}
	if n := claimsBy(b, bob); n != 1 {
		t.Fatalf("a's claim ended bob's live claim")
	}
}

// After a restart, what arrived while the daemon was down reaches sessions that were running
// before and reconnect, and nobody else.
func TestRestartCatchesUpReconnectingSessions(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	bob, _ := b.session("bbbbbb", "")
	eventually(t, "presence", func() bool { return a.seesSession(bob) })

	b.stop()
	a.do(proto.Request{Op: proto.OpSend, To: bob, Subject: "direct, during the restart"})
	a.do(proto.Request{Op: proto.OpSend, To: "*", Subject: "broadcast, during the restart"})
	b.start()
	b.helloAgain("bbbbbb", true)
	b.session("cccccc", "")

	var got []string
	eventually(t, "bob catches up", func() bool {
		for _, m := range b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages {
			got = append(got, m.Subject)
		}
		return len(got) >= 2
	})
	if len(got) != 2 {
		t.Fatalf("bob got %q", got)
	}
	for _, sess := range []string{"cccccc", proto.CLISession} {
		if ms := b.do(proto.Request{Op: proto.OpInbox, Session: sess}).Messages; len(ms) != 0 {
			t.Fatalf("%s got %+v", sess, ms)
		}
	}
}

func TestStaleLockIsCleared(t *testing.T) {
	remote := newRemote(t)
	a := newDevice(t, remote)
	work := filepath.Dir(a.gitDir)
	time.Sleep(time.Second) // past the first sync, which records tips without reading them

	// Another device's log appears on the remote, while a's copy of that ref has a lock left
	// behind by a killed git.
	ref := "refs/t4/mesh/zzz-0000"
	lock := filepath.Join(a.gitDir, filepath.FromSlash(ref)+".lock")
	os.MkdirAll(filepath.Dir(lock), 0o755)
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(lock, old, old)

	e := bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message, From: "zzz-0000/s", At: time.Now().UTC(),
		To: "*", Subject: "through the lock"}
	msg, _ := bus.Encode(e, e.Line())
	empty := git(t, work, "hash-object", "-t", "tree", "--stdin")
	sha := gitIn(t, work, msg, "commit-tree", empty)
	git(t, work, "push", "-q", remote, sha+":"+ref)

	eventually(t, "the event through the cleared lock", func() bool {
		for _, ev := range a.do(proto.Request{Op: proto.OpLog}).Events {
			if strings.Contains(ev.Line, "through the lock") {
				return true
			}
		}
		return false
	})
}
