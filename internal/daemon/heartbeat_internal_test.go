package daemon

import (
	"testing"
	"time"
)

// A heartbeat waits the heartbeat give or take a fifth, and not the same each time, so two devices
// whose daemons started together drift apart instead of pushing at the same moment every beat.
func TestHeartbeatIsJittered(t *testing.T) {
	t.Parallel()
	for _, beat := range []time.Duration{2 * time.Minute, 500 * time.Millisecond, 3} {
		lo, hi := beat-beat/5, beat+beat/5
		seen := map[time.Duration]bool{}
		lowest, highest := hi, lo
		for range 2000 {
			got := jittered(beat)
			if got < lo || got > hi {
				t.Fatalf("a heartbeat of %v waited %v, outside %v to %v", beat, got, lo, hi)
			}
			seen[got] = true
			lowest, highest = min(lowest, got), max(highest, got)
		}
		if beat < time.Microsecond {
			continue // too short to have a fifth worth spreading over
		}
		if len(seen) < 1000 {
			t.Errorf("a heartbeat of %v waited only %d different times in 2000", beat, len(seen))
		}
		// Spread over the whole range, not bunched at the beat.
		if lowest > beat-beat/6 || highest < beat+beat/6 {
			t.Errorf("a heartbeat of %v waited from %v to %v, want nearly %v to %v", beat, lowest, highest, lo, hi)
		}
	}
}
