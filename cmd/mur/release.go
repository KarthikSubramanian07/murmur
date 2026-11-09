package main

import (
	"strings"
	"sync"
	"time"

	"github.com/a-Fig/murmur/internal/checkin"
	"github.com/a-Fig/murmur/internal/update"
	"github.com/a-Fig/murmur/internal/web"
)

// releaseWatch is what the daemon knows of newer releases, for the page (#240): the updater's
// last check, and the daily check-in's answer for when the updater hasn't checked or can't.
type releaseWatch struct {
	running, gitDir string

	mu       sync.Mutex
	last     update.Status
	heard    bool      // the updater has reported a check
	checkin  string    // checkin.Newer, read at most once a minute
	readAt   time.Time // when checkin was read
	interval time.Duration
}

func newReleaseWatch(running, gitDir string) *releaseWatch {
	return &releaseWatch{running: running, gitDir: gitDir, interval: time.Minute}
}

// report is the updater's Report.
func (w *releaseWatch) report(s update.Status) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.last, w.heard = s, true
}

// page is the daemon's Version. It reads a file at most once a minute, and never the network.
func (w *releaseWatch) page() web.Version {
	v := web.Version{Running: w.running, Install: installLine()}
	if strings.HasSuffix(w.running, "-dev") {
		return v // a build from source is updated from source
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if now := time.Now(); now.Sub(w.readAt) >= w.interval {
		w.checkin, w.readAt = checkin.Newer(w.gitDir, w.running), now
	}
	latest := w.last.Latest
	// Once a release is installed, that is the one a restart starts, whatever came out since.
	if !w.last.Installed && w.checkin != "" && (latest == "" || update.Newer(w.checkin, latest)) {
		latest = w.checkin
	}
	if latest != "" && update.Newer(latest, w.running) {
		v.Latest = latest
	}
	// Until the updater's first check, which waits for the daemon's first sync, updates count as
	// on: they are unless turned off, and the first check says so within seconds.
	v.Auto = !w.heard || (!w.last.Off && !w.last.Failed)
	return v
}
