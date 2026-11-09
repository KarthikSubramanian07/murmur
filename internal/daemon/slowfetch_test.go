package daemon_test

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/proto"
)

// stallFetches makes this clone's fetches of anything large stop partway for a few seconds, as on a
// slow link: its upload-pack passes on the first reads, which hold a listing and a small fetch, then
// waits. dd because it reads no further than it passes on, unlike head on some systems.
func stallFetches(t *testing.T, gitDir string, stall string) {
	t.Helper()
	script := filepath.ToSlash(filepath.Join(t.TempDir(), "stall.sh"))
	body := "#!/bin/sh\ngit upload-pack \"$1\" | {\n" +
		"\treads=$(dd bs=8192 count=64 2>&1 >&3 | awk -F'[+ ]' 'NR == 1 { print $1 + $2 }')\n" +
		"\tif [ \"$reads\" = 64 ]; then sleep " + stall + "; cat; fi\n" +
		"} 3>&1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, filepath.Dir(gitDir), "config", "remote.origin.uploadpack", "sh '"+script+"'")
}

// pushLarge puts on ref a commit with message msg whose tree holds a large file, from outside Murmur.
func pushLarge(t *testing.T, remote, ref, msg string) {
	t.Helper()
	work := t.TempDir()
	git(t, work, "init", "-q")
	data := make([]byte, 4<<20)
	rand.Read(data) // so compression can't make it small
	file := filepath.Join(work, "large")
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	blob := git(t, work, "hash-object", "-w", "large")
	tree := gitIn(t, work, "100644 blob "+blob+"\tlarge\n", "mktree")
	sha := gitIn(t, work, msg, "commit-tree", tree)
	git(t, work, "push", "-q", remote, sha+":"+ref)
}

// A ref too large to fetch within a sync's limit fails neither that sync nor the next, and comes in
// once its own fetch outside the syncs has finished (#15). This is real git, cut off by the limit
// mid-download; a sync used to wait out its minute on such a ref and fail, at every poll.
func TestLargeRefLeavesTheSyncsWorking(t *testing.T) {
	remote := newRemote(t)
	var out syncBuf
	a := newDeviceWith(t, remote, func(d *device) {
		d.logTo = &out
		d.fetchLimit = time.Second
		d.stale = time.Minute
		stallFetches(t, d.gitDir, "4")
	})
	b := newDevice(t, remote, time.Minute)
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.session("bbbbbb", "")
	eventually(t, "a sees bob", func() bool { return a.seesSession(bob) })
	eventually(t, "b sees alice", func() bool { return b.seesSession(alice) })

	e := bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message, From: "zzz-0000/s", At: time.Now().UTC(),
		To: "*", Subject: "through the large object"}
	msg, err := bus.Encode(e, e.Line())
	if err != nil {
		t.Fatal(err)
	}
	pushLarge(t, remote, "refs/murmur/mesh/zzz-0000", msg)
	eventually(t, "the ref set aside", func() bool { return strings.Contains(out.String(), "apart from the syncs") })

	// The syncs go on without it: this message comes through a later sync than the one that set the
	// ref aside, while the ref's own fetch is still stalled or just past it.
	if r := b.send("bbbbbb", alice, "past the large object", ""); r.Error != "" {
		t.Fatalf("send: %+v", r)
	}
	logged := func(s string) bool {
		for _, ev := range a.do(proto.Request{Op: proto.OpLog}).Events {
			if strings.Contains(ev.Line, s) {
				return true
			}
		}
		return false
	}
	eventually(t, "the message past the large object", func() bool { return logged("past the large object") })

	eventually(t, "the large ref fetched", func() bool {
		return strings.Contains(out.String(), "fetched refs/murmur/mesh/zzz-0000")
	})
	eventually(t, "the event on the large ref", func() bool { return logged("through the large object") })
}
