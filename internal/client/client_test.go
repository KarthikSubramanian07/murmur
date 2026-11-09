package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/proc"
	"github.com/a-Fig/murmur/internal/proto"
)

func lockPath(t *testing.T) (gitDir, lock string) {
	t.Helper()
	gitDir = filepath.Join(t.TempDir(), ".git")
	if err := os.MkdirAll(StateDir(gitDir), 0o700); err != nil {
		t.Fatal(err)
	}
	return gitDir, filepath.Join(StateDir(gitDir), "daemon.lock")
}

// With no daemon there is nothing to wait for.
func TestWaitGoneWithNoDaemon(t *testing.T) {
	gitDir, _ := lockPath(t)
	if held, err := WaitGone(context.Background(), gitDir, time.Second); held || err != nil {
		t.Errorf("held %v, err %v", held, err)
	}
}

// A clone that never ran a daemon has no state directory, which is no daemon, not a failure: it is
// what `mur join --leave` meets in a clone that never joined.
func TestWaitGoneWithNoStateDirectory(t *testing.T) {
	gitDir := filepath.Join(t.TempDir(), ".git")
	if held, err := WaitGone(context.Background(), gitDir, time.Second); held || err != nil {
		t.Errorf("held %v, err %v", held, err)
	}
}

// A daemon that exits while it is waited for is reported as having been there, and gone.
func TestWaitGoneWaitsForADaemonToExit(t *testing.T) {
	gitDir, lock := lockPath(t)
	l, err := proc.Lock(lock)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		l.Close()
	}()
	if held, err := WaitGone(context.Background(), gitDir, 10*time.Second); !held || err != nil {
		t.Errorf("held %v, err %v", held, err)
	}
}

// A daemon still there at the deadline is an error, not a gone daemon.
func TestWaitGoneTimesOutOnADaemonThatStays(t *testing.T) {
	gitDir, lock := lockPath(t)
	l, err := proc.Lock(lock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if held, err := WaitGone(context.Background(), gitDir, 200*time.Millisecond); !held || !errors.Is(err, ErrStillRunning) {
		t.Errorf("held %v, err %v; want ErrStillRunning", held, err)
	}
}

// The page's address carries the page's token, not the daemon's (#42). A daemon from before the
// page had one has only the daemon's, which its page takes.
func TestPageURLTokens(t *testing.T) {
	for _, c := range []struct {
		name string
		info proto.Info
		want string
	}{
		{"own token", proto.Info{Web: "127.0.0.1:5000", Token: "sessions", PageToken: "page"}, "http://127.0.0.1:5000/#token=page"},
		{"older daemon", proto.Info{Web: "127.0.0.1:5000", Token: "sessions"}, "http://127.0.0.1:5000/#token=sessions"},
	} {
		got, err := (&Client{info: c.info}).PageURL()
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; want %q", c.name, got, err, c.want)
		}
	}
	if _, err := (&Client{info: proto.Info{Token: "sessions"}}).PageURL(); !errors.Is(err, ErrNoPage) {
		t.Errorf("no page: %v", err)
	}
}
