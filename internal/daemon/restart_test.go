package daemon_test

import (
	"context"
	"encoding/json"
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

func TestClaimsReleasedAfterLongOutage(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	bob, _ := b.session("bbbbbb", "")
	if r := b.do(proto.Request{Op: proto.OpClaim, Session: "bbbbbb", What: "search"}); r.Claimed == nil {
		t.Fatalf("claim: %+v", r)
	}
	eventually(t, "a sees the claim", func() bool { return len(a.openClaims()) == 1 })

	// b goes away for longer than the stale limit (faked by backdating its last presence push),
	// while its session keeps running and reconnects when the daemon is back.
	b.stop()
	statePath := filepath.Join(b.gitDir, "t4", "state.json")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	json.Unmarshal(raw, &st)
	st["lastPresence"] = time.Now().Add(-time.Hour).UTC()
	raw, _ = json.Marshal(st)
	if err := os.WriteFile(statePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	b.start()
	b.helloAgain("bbbbbb", true)
	eventually(t, "a sees bob again", func() bool { return a.seesSession(bob) })
	holds(t, "the claim staying ended", 2*time.Second, func() bool { return len(a.openClaims()) == 0 })
	got := b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages
	if len(got) != 1 || !strings.Contains(got[0].Subject, "has ended") {
		t.Fatalf("bob wasn't told: %+v", got)
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
