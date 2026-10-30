package daemon_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a-Fig/t4/internal/client"
	"github.com/a-Fig/t4/internal/daemon"
	"github.com/a-Fig/t4/internal/proto"
)

// consentClone is a clone of a fresh remote, and whether its user has joined (changed by the test).
func consentClone(t *testing.T) (remote, gitDir string, joined *atomic.Bool) {
	t.Helper()
	remote = newRemote(t)
	dir := filepath.Join(t.TempDir(), "clone")
	git(t, filepath.Dir(dir), "clone", "-q", remote, dir)
	joined = new(atomic.Bool)
	return remote, filepath.Join(dir, ".git"), joined
}

func runWithConsent(gitDir string, joined *atomic.Bool, poll time.Duration) (cancel func(), done chan error) {
	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, daemon.Options{GitDir: gitDir, Poll: 100 * time.Millisecond, Idle: -1,
			Consent: joined.Load, ConsentPoll: poll, Log: testLog()})
	}()
	return cancel, done
}

// A daemon starts only for a user who joined in the clone, and leaves nothing behind otherwise: no
// daemon.json to find, and nothing on the remote.
func TestDaemonWithoutConsentDoesNotStart(t *testing.T) {
	remote, gitDir, joined := consentClone(t)
	cancel, done := runWithConsent(gitDir, joined, 50*time.Millisecond)
	defer cancel()
	select {
	case err := <-done:
		if !errors.Is(err, daemon.ErrNoConsent) {
			t.Fatalf("got %v, want ErrNoConsent", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the daemon is running in a clone that hasn't joined")
	}
	if _, err := os.Stat(filepath.Join(gitDir, "t4", "daemon.json")); err == nil {
		t.Error("daemon.json was written")
	}
	if refs := git(t, remote, "for-each-ref", "refs/t4"); refs != "" {
		t.Errorf("the remote has bus refs:\n%s", refs)
	}
}

// A user who leaves while their daemon runs, or is still starting, stops it: the daemon watches the
// consent itself, so a leave needn't find it to end it.
func TestDaemonStopsWhenTheUserLeaves(t *testing.T) {
	_, gitDir, joined := consentClone(t)
	joined.Store(true)
	cancel, done := runWithConsent(gitDir, joined, 50*time.Millisecond)
	defer cancel()
	var c *client.Client
	eventually(t, "the daemon to come up", func() bool {
		var err error
		c, err = client.Connect(context.Background(), gitDir, false)
		return err == nil
	})
	if resp, err := c.Do(context.Background(), proto.Request{Op: proto.OpWho}); err != nil || resp.Error != "" {
		t.Fatalf("who while joined: %v %q", err, resp.Error)
	}

	joined.Store(false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stopped with %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the daemon kept running after the user left")
	}
	if _, err := os.Stat(filepath.Join(gitDir, "t4", "daemon.json")); err == nil {
		t.Error("daemon.json is still there")
	}
}

// Between the user leaving and the daemon noticing, it takes no registration and answers no request.
func TestDaemonRefusesRequestsOnceTheUserHasLeft(t *testing.T) {
	_, gitDir, joined := consentClone(t)
	joined.Store(true)
	cancel, done := runWithConsent(gitDir, joined, time.Hour) // the watch never fires: only the checks on requests
	defer cancel()
	var c *client.Client
	eventually(t, "the daemon to come up", func() bool {
		var err error
		c, err = client.Connect(context.Background(), gitDir, false)
		return err == nil
	})

	joined.Store(false)
	ctx := context.Background()
	if resp, err := c.Do(ctx, proto.Request{Op: proto.OpStatus, Session: "abc123", Doing: "x"}); err != nil || !strings.Contains(resp.Error, "hasn't joined") {
		t.Errorf("status after leaving: %v, %q", err, resp.Error)
	}
	if conn, _, err := c.Hello(ctx, proto.Hello{ID: "abc123", Agent: "test", Cwd: filepath.Dir(gitDir)}); err == nil || !strings.Contains(err.Error(), "hasn't joined") {
		if conn != nil {
			conn.Close()
		}
		t.Errorf("hello after leaving: %v", err)
	}
	// Finding the daemon and stopping it are always allowed: that is how a leave ends it. Stop
	// connects with a ping, so a daemon that refused the ping would never be sent the stop.
	if stopped, err := client.Stop(ctx, gitDir); err != nil || !stopped {
		t.Errorf("stop after leaving: stopped %v, err %v", stopped, err)
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("stop didn't stop it")
	}
}

// The loops that publish on their own must not publish for a user who has left, in the moment
// before the watcher stops the daemon (here the watcher never fires). While joined, a presence ref
// that is deleted from the remote is pushed again within a heartbeat, which shows the test can see a
// publication; once the user has left it is not.
func TestDaemonPublishesNothingOnceTheUserHasLeft(t *testing.T) {
	remote, gitDir, joined := consentClone(t)
	joined.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, daemon.Options{GitDir: gitDir, Poll: 100 * time.Millisecond, Heartbeat: 100 * time.Millisecond,
			Idle: -1, Consent: joined.Load, ConsentPoll: time.Hour, Log: testLog()})
	}()
	defer func() { cancel(); <-done }()

	busRefs := func() []string {
		out := git(t, remote, "for-each-ref", "--format=%(refname)", "refs/t4")
		if out == "" {
			return nil
		}
		return strings.Split(out, "\n")
	}
	clearBus := func() {
		for _, ref := range busRefs() {
			git(t, remote, "update-ref", "-d", ref)
		}
	}
	eventually(t, "the daemon's presence to be on the remote", func() bool { return len(busRefs()) > 0 })
	clearBus()
	eventually(t, "the presence to be pushed again while joined", func() bool { return len(busRefs()) > 0 })

	joined.Store(false)
	clearBus()
	time.Sleep(2 * time.Second) // twenty heartbeats
	if refs := busRefs(); len(refs) != 0 {
		t.Errorf("the daemon published after the user left: %q", refs)
	}
}
