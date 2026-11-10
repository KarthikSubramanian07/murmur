package daemon_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/proto"
)

// holdListing makes every listing and fetch of this clone's remote wait until release exists, once
// held exists to say one is waiting: a remote that takes as long as the test likes to answer.
func holdListing(t *testing.T, gitDir, held, release string) {
	t.Helper()
	script := filepath.ToSlash(filepath.Join(t.TempDir(), "hold.sh"))
	body := "#!/bin/sh\n: > '" + filepath.ToSlash(held) + "'\n" +
		"while [ ! -f '" + filepath.ToSlash(release) + "' ]; do sleep 0.1; done\n" +
		"exec git upload-pack \"$1\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, filepath.Dir(gitDir), "config", "remote.origin.uploadpack", "sh '"+script+"'")
}

// A daemon answers before its first sync is over, so whoever started it isn't kept waiting on the
// remote (#372: `mur ui` gave up after 15 s on a daemon still in its first sync). Who waits for that
// sync, as before it the daemon knows nothing of a device it has never seen.
func TestDaemonAnswersBeforeItsFirstSync(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	b := newDevice(t, remote, time.Minute)
	bob, _ := b.session("bbbbbb", "")
	eventually(t, "b's presence on the remote", func() bool {
		return strings.Contains(git(t, remote, "for-each-ref", brand.PresencePrefix), brand.PresencePrefix+b.self())
	})

	tmp := t.TempDir()
	held, release := filepath.Join(tmp, "held"), filepath.Join(tmp, "release")
	var out syncBuf
	dumpLogOnFailure(t, &out)
	a := newDeviceWith(t, remote, func(d *device) {
		d.logTo = &out
		holdListing(t, d.gitDir, held, release)
		d.firstSync = 10 * time.Minute // the hold, not the limit, decides when the sync ends
	})
	// After a's start, so it runs before a stops: a git left waiting would hold the daemon's exit.
	t.Cleanup(func() { os.WriteFile(release, nil, 0o644) })
	eventually(t, "a's first sync to wait on the remote", func() bool {
		_, err := os.Stat(held)
		return err == nil
	})
	if self := a.self(); self == "" {
		t.Fatal("a didn't answer a ping during its first sync")
	}
	if strings.Contains(out.String(), "first sync") {
		t.Fatal("a answered only once its first sync had given up")
	}

	type answer struct {
		resp proto.Response
		err  error
	}
	who := make(chan answer, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		resp, err := a.c.Do(ctx, proto.Request{Op: proto.OpWho})
		who <- answer{resp, err}
	}()
	// A registration waits too: what the first sync reads is the backlog, not for a new session.
	hello := make(chan error, 1)
	go func() {
		conn, _, err := a.c.Hello(context.Background(), proto.Hello{ID: "aaaaaa", Agent: "test"})
		if conn != nil {
			t.Cleanup(func() { conn.Close() })
		}
		hello <- err
	}()
	select {
	case got := <-who:
		t.Fatalf("who answered before the first sync was over: %+v, %v", got.resp, got.err)
	case err := <-hello:
		t.Fatalf("a session registered before the first sync was over: %v", err)
	case <-time.After(time.Second):
	}
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var got answer
	select {
	case got = <-who:
	case <-time.After(time.Minute):
		dumpGoroutines(t)
		t.Fatal("who didn't answer once the first sync could finish")
	}
	select {
	case err := <-hello:
		if err != nil {
			t.Fatalf("hello once the first sync could finish: %v", err)
		}
	case <-time.After(time.Minute):
		t.Fatal("the session didn't register once the first sync could finish")
	}
	if got.err != nil || got.resp.Error != "" {
		t.Fatalf("who: %v %q", got.err, got.resp.Error)
	}
	for _, dv := range got.resp.Devices {
		for _, s := range dv.Sessions {
			if s.Addr == bob {
				return
			}
		}
	}
	t.Fatalf("who, answered after the first sync, doesn't show bob (%s): %+v", bob, got.resp.Devices)
}
