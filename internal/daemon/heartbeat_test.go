package daemon_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A device with nothing happening still pushes its presence on every heartbeat, or it would look
// stale to everyone 6 minutes after it started. The heartbeat is a timer that each beat sets
// again, at random within a fifth of the beat (see jittered), and one that isn't set again fires
// once and never more.
func TestPresenceKeepsBeating(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a := newDeviceWith(t, remote, func(d *device) { d.beat = 200 * time.Millisecond })
	ref := "refs/murmur/presence/" + a.self()
	seen := map[string]bool{}
	eventually(t, "five presence pushes", func() bool {
		if sha, _, ok := strings.Cut(git(t, filepath.Dir(remote), "ls-remote", remote, ref), "\t"); ok {
			seen[sha] = true
		}
		return len(seen) >= 5
	})
}
