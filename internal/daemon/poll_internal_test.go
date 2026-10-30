package daemon

import (
	"testing"
	"time"
)

// A failing poll waits twice as long each time, to at most a minute (or 20 polls where Poll is
// short), and one that works is back to Poll at once.
func TestNextPollBacksOffAndRecovers(t *testing.T) {
	base := 3 * time.Second
	wait := base
	var got []time.Duration
	for range 8 {
		wait = nextPoll(wait, base, true)
		got = append(got, wait)
	}
	want := []time.Duration{6 * time.Second, 12 * time.Second, 24 * time.Second, 48 * time.Second, time.Minute, time.Minute, time.Minute, time.Minute}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("after %d failures the wait is %s, want %s (all: %v)", i+1, got[i], want[i], got)
		}
	}
	if w := nextPoll(time.Minute, base, false); w != base {
		t.Errorf("after a success the wait is %s, want %s", w, base)
	}

	// A short Poll, as the tests use, doesn't back off to a minute: 20 polls at most.
	short := 50 * time.Millisecond
	wait = short
	for range 10 {
		wait = nextPoll(wait, short, true)
	}
	if wait != 20*short {
		t.Errorf("a short poll backs off to %s, want %s", wait, 20*short)
	}

	// A Poll longer than the cap is never shortened by it.
	long := 90 * time.Second
	if w := nextPoll(long, long, true); w != long {
		t.Errorf("a poll of %s waits %s after a failure", long, w)
	}
}
