package daemon

import (
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/t3db"
)

// A session whose hook has no turn going is at work while T3 has a run of its thread going that
// the hook hasn't seen start (#607): one a read found after the turn ended, or one the daemon just
// started by handing a message over.
func TestARunTheHookHasntSeenIsAtWork(t *testing.T) {
	t.Parallel()
	const ended = 10 * time.Second // ago
	busy, idle := &t3db.State{Busy: true}, &t3db.State{}
	for _, tc := range []struct {
		name   string
		hookOn bool
		state  *t3db.State   // nil: never read
		read   time.Duration // ago, when the last read began
		handed time.Duration // ago; 0: not handed over
		want   bool
	}{
		{"a run, read after the turn ended", true, busy, 2 * time.Second, 0, true},
		{"a run, read before the turn ended", true, busy, 20 * time.Second, 0, false},
		{"no run, read after the turn ended", true, idle, 2 * time.Second, 0, false},
		{"handed over, not read since", true, idle, 5 * time.Second, 2 * time.Second, true},
		{"handed over, thread never read", true, nil, 0, 2 * time.Second, true},
		{"handed over, a read since finds no run", true, idle, time.Second, 2 * time.Second, false},
		{"handed over, a read since finds the run", true, busy, time.Second, 2 * time.Second, true},
		{"handed over before the turn ended", true, idle, 20 * time.Second, 15 * time.Second, false},
		{"handed over past handedHold, never read", true, nil, 0, handedHold + time.Second, false},
		// With no hook, T3's queue is the only way the session hears of a message.
		{"no hook, a run", false, busy, 2 * time.Second, 2 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := threadsDaemon(t)
			inT3(d, "aaaaaa", time.Hour)
			now := time.Now()
			s := d.sessions["aaaaaa"]
			s.hooked, s.stopSeen, s.endedAt = tc.hookOn, true, now.Add(-ended)
			k := threadKey{"", "thread-aaaaaa"}
			if tc.state != nil {
				d.threads[k], d.threadRead[k] = *tc.state, now.Add(-tc.read)
			}
			if tc.handed > 0 {
				s.handedAt = now.Add(-tc.handed)
			}
			d.mu.Lock()
			defer d.mu.Unlock()
			if got := d.hookBusyLocked("aaaaaa"); got != tc.want {
				t.Errorf("hookBusyLocked = %v, want %v", got, tc.want)
			}
		})
	}
}
