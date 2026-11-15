package daemon_test

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/clones"
)

// recordAtListen reads the record of this machine's clones when the daemon logs where it listens,
// which it does just after it starts answering and writes daemon.json.
type recordAtListen struct {
	record, gitDir string

	mu       sync.Mutex
	logged   bool
	answered bool // daemon.json was there
	list     []clones.Clone
	err      error
}

func (r *recordAtListen) Write(p []byte) (int, error) {
	if !bytes.Contains(p, []byte(", listening on ")) {
		return len(p), nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.logged {
		r.logged = true
		_, err := os.Stat(filepath.Join(r.gitDir, brand.StateDir, "daemon.json"))
		r.answered = err == nil
		r.list, r.err = clones.List(r.record, func(string) bool { return true })
	}
	return len(p), nil
}

// A daemon is on the record of this machine's clones, under its repository's name, before it
// answers (#479). `mur join` writes the record, starts the daemon and returns once it answers, and
// another clone's page lists the record; were the daemon's write after that, a read could find the
// join's version being replaced, which on Windows can't be read, and list no clone at all.
func TestTheDaemonIsOnTheRecordOfClonesBeforeItAnswers(t *testing.T) {
	remote := newRemote(t)
	at := &recordAtListen{record: filepath.Join(t.TempDir(), "clones")}
	d := newDeviceWith(t, remote, func(d *device) {
		at.gitDir = d.gitDir
		d.clones, d.logTo, d.noKey = at.record, at, true
	})
	at.mu.Lock()
	defer at.mu.Unlock()
	if !at.logged || !at.answered {
		t.Fatalf("the daemon didn't log where it listens after writing daemon.json (logged %v, daemon.json %v)", at.logged, at.answered)
	}
	if at.err != nil || len(at.list) != 1 || at.list[0].GitDir != filepath.Clean(d.gitDir) || at.list[0].Name != "remote" {
		t.Errorf("the record as the daemon answered: %+v, %v; want this clone, named remote", at.list, at.err)
	}
}
