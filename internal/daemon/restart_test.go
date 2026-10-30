package daemon_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
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

// A daemon that stops deletes its presence, so others drop it at their next sync rather than
// when it goes stale.
func TestGoodbyeDropsDeviceAtOnce(t *testing.T) {
	remote := newRemote(t)
	// A real stale limit: the goodbye must drop the device long before it would go stale.
	a, b := newDevice(t, remote, time.Minute), newDevice(t, remote, time.Minute)
	eventually(t, "a sees b", func() bool { return a.seesDevices(2) })
	b.stop()
	start := time.Now()
	eventually(t, "b gone from a", func() bool { return a.seesDevices(1) })
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("took %s", took)
	}
}

func (d *device) offline(remote string, off bool) {
	url := remote
	if off {
		url = filepath.Join(filepath.Dir(remote), "nowhere.git")
	}
	git(d.t, filepath.Dir(d.gitDir), "config", "remote.origin.url", url)
}

// A reader that can't reach the remote (asleep, offline) has no grounds to call anyone else
// gone: it judges staleness only up to its own last successful sync.
func TestOfflineReaderCallsNobodyStale(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	bdev := b.self()
	eventually(t, "b on a", func() bool { return a.fresh(bdev) })
	a.offline(remote, true)
	holds(t, "b fresh on a while a is offline", 2*a.stale, func() bool { return a.fresh(bdev) })
	a.offline(remote, false)
	holds(t, "b fresh on a once a is back", time.Second, func() bool { return a.fresh(bdev) })
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

// A message that lands after a restarted daemon's first sync, but before a session that was
// running reconnects, still reaches it.
func TestMessageBeforeReconnectIsCaughtUp(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	bob, _ := b.session("bbbbbb", "")
	eventually(t, "presence", func() bool { return a.seesSession(bob) })
	b.restart() // returns once the new daemon is listening
	a.do(proto.Request{Op: proto.OpSend, To: bob, Subject: "sent while bob reconnects"})
	eventually(t, "b reads it", func() bool {
		for _, ev := range b.do(proto.Request{Op: proto.OpLog}).Events {
			if strings.Contains(ev.Line, "sent while bob reconnects") {
				return true
			}
		}
		return false
	})
	b.helloAgain("bbbbbb", true)
	if got := b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages; len(got) != 1 {
		t.Fatalf("bob got %d messages, want the one sent while he reconnected", len(got))
	}
}

// When a daemon's first sync fails, sessions register before anything is settled. What was then
// sent after they started still reaches them; the backlog doesn't.
func TestFirstSyncFailedStillDelivers(t *testing.T) {
	remote := newRemote(t)
	a := newDevice(t, remote)
	pushEvent(t, remote, "refs/t4/mesh/old-0001", bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message,
		From: "old-0001/x", At: time.Now().Add(-10 * time.Minute).UTC(), To: "*", Subject: "backlog"})
	dir := filepath.Join(t.TempDir(), "clone")
	git(t, filepath.Dir(dir), "clone", "-q", "--no-hardlinks", remote, dir)
	// Pushes go out, so presence is seen, but listing and fetching fail.
	git(t, dir, "config", "remote.origin.pushurl", remote)
	git(t, dir, "config", "remote.origin.url", filepath.Join(filepath.Dir(remote), "nowhere.git"))
	b := &device{t: t, gitDir: filepath.Join(dir, ".git"), stale: 3 * time.Second, poll: 100 * time.Millisecond}
	b.start()
	bob, _ := b.session("bbbbbb", "")
	eventually(t, "a sees bob", func() bool { return a.seesSession(bob) })

	a.do(proto.Request{Op: proto.OpSend, To: "*", Subject: "broadcast"})
	a.do(proto.Request{Op: proto.OpSend, To: bob, Subject: "direct"})
	git(t, dir, "config", "remote.origin.url", remote)

	var got []string
	eventually(t, "bob gets both", func() bool {
		for _, m := range b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages {
			got = append(got, m.Subject)
		}
		return len(got) >= 2
	})
	sort.Strings(got)
	if strings.Join(got, ",") != "broadcast,direct" {
		t.Fatalf("bob got %q, want the broadcast and the direct message", got)
	}
}
