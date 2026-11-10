package main

import (
	"context"
	"time"

	"github.com/a-Fig/murmur/internal/web"
)

// Health is a device that works now and lost the remote for a while earlier: the sync's last error
// is kept after it recovered, and a few pushes were refused meanwhile.
func (f *fixtures) Health(ctx context.Context) (web.Health, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	return web.Health{
		At: now,
		Sync: web.SyncHealth{LastOK: ago(2 * time.Second), LastOKAge: 2, Syncs: 1840, Failed: 37,
			LastError: &web.HealthError{Class: web.ErrDNS, At: ago(40 * time.Minute),
				Message: "fatal: unable to access 'https://github.com/a-Fig/murmur/': Could not resolve host: github.com"}},
		Pushes: web.PushesHealth{
			Mesh:     web.PushHealth{Pushed: 64, LeaseRefused: 1},
			Presence: web.PushHealth{Pushed: 212, Failed: 7, LastError: &web.HealthError{Class: web.ErrDNS, At: ago(41 * time.Minute), Message: "fatal: unable to access 'https://github.com/a-Fig/murmur/': Could not resolve host: github.com"}},
			Names:    web.PushHealth{Pushed: 9, LeaseRefused: 2},
		},
		Fetches: web.FetchHealth{Refs: []web.RefHealth{}},
		Delivery: web.DeliveryHealth{
			T3:   web.HandoffHealth{Tried: 21, Handed: 20, Failed: 1},
			Hook: web.HookHealth{Whole: 3, Notices: 11},
		},
		Daemon: web.DaemonHealth{Started: ago(3 * time.Hour), Version: f.version.Running, Protocol: 8,
			Poll: web.PollSlow, PollEvery: 12, UpdateChecked: ago(20 * time.Minute)},
		PublicRepo: web.PublicRepoHealth{State: web.RepoReported, Repo: "github.com/a-fig/murmur"},
	}, nil
}
