package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/proc"
)

// A daemon still starting holds the clone's lock before it writes daemon.json (#668). Stop waits
// for it rather than say no daemon is running: here it lets go without ever being reachable, as one
// whose start failed does, and Stop then says there was none; one that holds on past the wait is an
// error, never "no daemon".
func TestStopWaitsForADaemonStillStarting(t *testing.T) {
	gitDir := t.TempDir()
	if err := os.MkdirAll(StateDir(gitDir), 0o700); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(StateDir(gitDir), "daemon.lock")
	held, err := proc.Lock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	const hold = 400 * time.Millisecond
	go func() { time.Sleep(hold); held.Close() }()
	start := time.Now()
	stopped, err := Stop(context.Background(), gitDir)
	if err != nil || stopped {
		t.Fatalf("stopped=%v err=%v", stopped, err)
	}
	if took := time.Since(start); took < hold {
		t.Errorf("Stop returned after %v, before the starting daemon let go of the lock", took)
	}

	held, err = proc.Lock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	old := startWait
	startWait = 300 * time.Millisecond
	defer func() { startWait = old }()
	if stopped, err := Stop(context.Background(), gitDir); !errors.Is(err, ErrStillRunning) || stopped {
		t.Errorf("a daemon that stays starting: stopped=%v err=%v", stopped, err)
	}
}
