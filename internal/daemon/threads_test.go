package daemon_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/t3db"
)

// The wake setting's "within N minutes" (#25) and settled T3 threads going offline (#189), end to
// end, with a stand-in for T3's database.

// fakeThreads is the stand-in: each thread's state, as the test sets it.
type fakeThreads struct {
	mu sync.Mutex
	st map[string]t3db.State
}

func (f *fakeThreads) set(thread string, st t3db.State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.st[thread] = st
}

func (f *fakeThreads) read(_ context.Context, _, _, thread string) (t3db.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.st[thread]
	if !ok {
		return t3db.State{}, t3db.ErrNotFound
	}
	return st, nil
}

// trySend sends as session id, and returns the answer, refusal or not.
func (d *device) trySend(id, to, subject string) proto.Response {
	d.t.Helper()
	return d.do(proto.Request{Op: proto.OpSend, Session: id, To: to, Subject: subject})
}

// newT3Device is newDevice whose T3 database is threads.
func newT3Device(t *testing.T, remote string, threads *fakeThreads) *device {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	git(t, filepath.Dir(dir), "clone", "-q", "--no-local", remote, dir)
	d := &device{t: t, gitDir: filepath.Join(dir, ".git"), stale: 3 * time.Second, poll: 100 * time.Millisecond,
		threadState: threads.read}
	d.start()
	return d
}

// Under the default, within 3 minutes, a thread idle longer is not woken: the message waits as a
// notice. Once the thread is at work again, it goes in as the next turn.
func TestWithinLeavesALongIdleThreadAlone(t *testing.T) {
	remote := newRemote(t)
	exe, calls := useFakeT3(t)
	threads := &fakeThreads{st: map[string]t3db.State{"thread-bob": {LastRun: time.Now().Add(-10 * time.Minute)}}}
	a, b := newDevice(t, remote), newT3Device(t, remote, threads)
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.sessionRoute("bbbbbb", "claude-bob", proto.Route{T3CLI: exe, T3Thread: "thread-bob"})
	eventually(t, "presence", func() bool { return a.seesSession(bob) && b.seesSession(alice) })

	a.send("aaaaaa", bob, "not now", "")
	eventually(t, "the message to reach b", func() bool { return logHas(b, "not now") })
	holds(t, "no delivery to a thread idle 10 minutes", time.Second, func() bool { return len(calls()) == 0 })

	threads.set("thread-bob", t3db.State{Busy: true})
	eventually(t, "delivery once the thread is at work", func() bool { return len(calls()) == 1 })
	if !strings.Contains(calls()[0].Stdin, "not now") {
		t.Errorf("the delivered turn: %q", calls()[0].Stdin)
	}

	threads.set("thread-bob", t3db.State{LastRun: time.Now()})
	a.send("aaaaaa", bob, "just now", "")
	eventually(t, "delivery to a thread idle a moment", func() bool { return len(calls()) == 2 })
}

// A settled thread is offline: it leaves the other device's who, a message to it is refused, and
// it is back once unsettled.
func TestSettledThreadIsOfflineAndRefusesMessages(t *testing.T) {
	remote := newRemote(t)
	exe, calls := useFakeT3(t)
	threads := &fakeThreads{st: map[string]t3db.State{"thread-bob": {LastRun: time.Now()}}}
	a, b := newDevice(t, remote), newT3Device(t, remote, threads)
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.sessionRoute("bbbbbb", "claude-bob", proto.Route{T3CLI: exe, T3Thread: "thread-bob"})
	bobName := b.named(bob)
	eventually(t, "presence", func() bool { return a.seesSession(bob) && b.seesSession(alice) })

	threads.set("thread-bob", t3db.State{Settled: true, LastRun: time.Now()})
	eventually(t, "bob to go offline", func() bool { return !a.seesSession(bob) })
	resp := a.trySend("aaaaaa", bobName, "anyone there?")
	if resp.OK || !strings.Contains(resp.Error, "settled") {
		t.Errorf("a send to a settled thread: ok %v, error %q", resp.OK, resp.Error)
	}
	resp = a.trySend("aaaaaa", bob, "by address")
	if resp.OK || !strings.Contains(resp.Error, "settled") {
		t.Errorf("a send to a settled thread by address: ok %v, error %q", resp.OK, resp.Error)
	}
	// From its own device too.
	b.session("cccccc", "")
	if resp := b.trySend("cccccc", bobName, "local"); resp.OK || !strings.Contains(resp.Error, "settled") {
		t.Errorf("a local send to a settled thread: ok %v, error %q", resp.OK, resp.Error)
	}
	holds(t, "nothing delivered to a settled thread", 500*time.Millisecond, func() bool { return len(calls()) == 0 })

	threads.set("thread-bob", t3db.State{LastRun: time.Now()})
	eventually(t, "bob to be back", func() bool { return a.seesSession(bob) })
	if resp := a.send("aaaaaa", bobName, "welcome back", ""); !resp.OK {
		t.Fatalf("a send once unsettled: %q", resp.Error)
	}
	eventually(t, "delivery once unsettled", func() bool { return len(calls()) == 1 })
}
