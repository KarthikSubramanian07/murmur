package daemon

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
)

// A write of a session's start can land and still be reported as failed, when the check bus.Push
// makes after the error fails too (#434, Sol's review of #432). Tried again, it isn't written a
// second time: the retry looks for the commit it pushed on the remote's log first. That holds
// when another write has since built on it, and a commit that never landed is written as usual.
func TestAStartReportedFailedButLandedIsNotWrittenTwice(t *testing.T) {
	t.Parallel()
	d, _, clone := rateRig(t, Options{}, false)
	remote := filepath.Join(filepath.Dir(clone), "remote.git")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ref := d.meshRef()
	count := func() string {
		return strings.TrimSpace(runGit(t, remote, "rev-list", "--count", ref))
	}

	// What the first write did: a commit pushed, which landed, and an error back. ownTip didn't move.
	start := bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.SessionStarted, From: bus.Addr(d.device, "aaaaaa"),
		At: time.Now().UTC(), Agent: "test", Branch: "main"}
	d.mu.Lock()
	boxed, err := d.encryptEventLocked(start)
	d.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	msg, err := bus.Encode(boxed, bus.EventSubject)
	if err != nil {
		t.Fatal(err)
	}
	landed, err := d.repo.NewCommit(ctx, d.ownTip, msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.repo.Push(ctx, landed, ref, bus.FastForward, ""); err != nil {
		t.Fatal(err)
	}
	// A message goes out meanwhile: its push is refused, as the log moved, and it builds on the start.
	if err := d.appendEvent(ctx, bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message,
		From: bus.Addr(d.device, "aaaaaa"), To: "*", At: time.Now().UTC(), Subject: "hello"}); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != "2" {
		t.Fatalf("%s commits on the log after the start and a message, want 2", n)
	}

	d.mu.Lock()
	d.st.Owed = []owedEvent{{Event: start, Tried: landed}}
	d.mu.Unlock()
	if err := d.writeOwed(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != "2" {
		t.Fatalf("%s commits on the log after the start was tried again, want 2: it was written twice", n)
	}
	d.mu.Lock()
	owed := len(d.st.Owed)
	d.mu.Unlock()
	if owed != 0 {
		t.Fatalf("%d starts and stops still owed, want none", owed)
	}

	// A stop whose first push never landed: its commit was made, and is nowhere on the remote.
	stop := bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.SessionStopped, From: bus.Addr(d.device, "aaaaaa"),
		At: time.Now().UTC()}
	lost, err := d.repo.NewCommit(ctx, landed, msg)
	if err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	d.st.Owed = []owedEvent{{Event: stop, Tried: lost}}
	d.mu.Unlock()
	if err := d.writeOwed(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != "3" {
		t.Fatalf("%s commits on the log after the stop, want 3", n)
	}
}
