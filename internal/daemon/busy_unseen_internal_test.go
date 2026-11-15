package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/proto"
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

// A run no hook has shown going stops counting after unseenHold: T3 can leave one queued for good,
// and the session would never be told of its messages. Until then, the hook's first call dates the
// turn from when the run was first seen, so what came since is held for the turn's end however
// long it waited.
func TestAnUnseenRunCountsForUnseenHoldAndStartsTheTurn(t *testing.T) {
	t.Parallel()
	d := threadsDaemon(t)
	inT3(d, "aaaaaa", time.Hour)
	s := d.sessions["aaaaaa"]
	s.claudeSession = "claude-a"
	now := time.Now()
	s.hooked, s.stopSeen, s.endedAt = true, true, now.Add(-time.Hour)
	k := threadKey{"", "thread-aaaaaa"}
	d.threads[k], d.threadRead[k] = t3db.State{Busy: true}, now.Add(-20*time.Minute)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.noteUnseenLocked()
	if !s.unseenAt.Equal(d.threadRead[k]) {
		t.Fatalf("unseenAt = %v, want the read's %v", s.unseenAt, d.threadRead[k])
	}
	d.threadRead[k] = now // read again since: the run still goes
	d.noteUnseenLocked()
	if !s.unseenAt.Equal(now.Add(-20 * time.Minute)) {
		t.Errorf("a later read moved unseenAt to %v", s.unseenAt)
	}
	if d.hookBusyLocked("aaaaaa") {
		t.Error("at work by a run no hook has shown going for 20 minutes")
	}

	s.unseenAt = now.Add(-time.Minute)
	if !d.hookBusyLocked("aaaaaa") {
		t.Fatal("not at work by a run first seen a minute ago")
	}
	if at := d.nextLapseLocked(); !at.Equal(s.unseenAt.Add(unseenHold)) {
		t.Errorf("nextLapseLocked = %v, want when the run stops counting, %v", at, s.unseenAt.Add(unseenHold))
	}
	d.touchHookLocked(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-a"})
	if !s.turnStart.Equal(s.unseenAt) {
		t.Errorf("turnStart = %v, want when the run was first seen, %v", s.turnStart, s.unseenAt)
	}
}

// The push loop wakes when a handoff's hold lapses, as nothing else may come to let the session's
// next message go.
func TestAHandoffsHoldWakesThePushLoop(t *testing.T) {
	t.Parallel()
	d := threadsDaemon(t)
	inT3(d, "aaaaaa", time.Hour)
	s := d.sessions["aaaaaa"]
	s.hooked, s.handedAt = true, time.Now().Add(-time.Second)
	d.mu.Lock()
	defer d.mu.Unlock()
	if at := d.nextLapseLocked(); !at.Equal(s.handedAt.Add(handedHold)) {
		t.Errorf("nextLapseLocked = %v, want %v", at, s.handedAt.Add(handedHold))
	}
	s.handedAt = time.Now().Add(-handedHold - time.Second)
	if at := d.nextLapseLocked(); !at.IsZero() {
		t.Errorf("nextLapseLocked = %v after the hold lapsed, want zero", at)
	}
}

// A read that finds no run ends an unseen run, as Stop would: its date mustn't stand until the next
// Stop, when it would have lapsed the cap for a run that starts later and dated that turn from
// hours before. A run is dated from when T3 took its prompt, where the read says.
func TestAnUnseenRunThatEndsWithoutAHookIsForgotten(t *testing.T) {
	t.Parallel()
	d := threadsDaemon(t)
	inT3(d, "aaaaaa", time.Hour)
	s := d.sessions["aaaaaa"]
	now := time.Now()
	s.hooked, s.stopSeen, s.endedAt = true, true, now.Add(-2*time.Hour)
	s.unseenAt = s.endedAt.Add(200 * time.Millisecond) // a read caught T3 closing the run
	k := threadKey{"", "thread-aaaaaa"}
	d.threads[k], d.threadRead[k] = t3db.State{}, now.Add(-time.Hour)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.noteUnseenLocked()
	if !s.endedAt.Equal(d.threadRead[k]) || !s.unseenAt.IsZero() {
		t.Fatalf("after a read finds no run: endedAt %v, unseenAt %v; want the read's %v, zero", s.endedAt, s.unseenAt, d.threadRead[k])
	}

	// Later, a prompt: T3 took it 3 s before the read that finds the run.
	took := now.Add(-3 * time.Second)
	d.threads[k], d.threadRead[k] = t3db.State{Busy: true, LastRun: took}, now
	d.noteUnseenLocked()
	if !s.unseenAt.Equal(took) {
		t.Errorf("unseenAt = %v, want when T3 took the prompt, %v", s.unseenAt, took)
	}
	if !d.hookBusyLocked("aaaaaa") {
		t.Error("not at work while the new run goes")
	}
}

// A read within closeGrace of the turn's end may find the run Stop ended still open: it says
// nothing of a new turn.
func TestARunReadJustAfterStopIsTheOldOne(t *testing.T) {
	t.Parallel()
	d := threadsDaemon(t)
	inT3(d, "aaaaaa", time.Hour)
	s := d.sessions["aaaaaa"]
	now := time.Now()
	s.hooked, s.stopSeen, s.endedAt = true, true, now.Add(-500*time.Millisecond)
	k := threadKey{"", "thread-aaaaaa"}
	d.threads[k], d.threadRead[k] = t3db.State{Busy: true}, now
	d.mu.Lock()
	defer d.mu.Unlock()
	d.noteUnseenLocked()
	if d.hookBusyLocked("aaaaaa") || !s.unseenAt.IsZero() {
		t.Errorf("busy %v, unseenAt %v, by a read half a second after Stop", d.hookBusyLocked("aaaaaa"), s.unseenAt)
	}
}

// Just before a handoff to T3 the thread is read again: a prompt T3 took since the last read keeps
// the message from being queued behind its run. The read stands for the next.
func TestAHandoffReadsTheThreadFirst(t *testing.T) {
	t.Parallel()
	d := threadsDaemon(t)
	inT3(d, "aaaaaa", time.Hour)
	s := d.sessions["aaaaaa"]
	s.hooked, s.stopSeen, s.endedAt = true, true, time.Now().Add(-time.Minute)
	st := t3db.State{}
	d.opt.ThreadState = func(context.Context, string, string, string) (t3db.State, error) { return st, nil }
	j := &pushJob{id: "aaaaaa", route: s.route}
	if d.t3RunGoing(context.Background(), j) {
		t.Fatal("a run going, by a read that found none")
	}
	st = t3db.State{Busy: true}
	if !d.t3RunGoing(context.Background(), j) {
		t.Fatal("no run going, by a read that found one")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if k := (threadKey{"", "thread-aaaaaa"}); !d.threads[k].Busy || d.threadRead[k].IsZero() || s.unseenAt.IsZero() {
		t.Errorf("the read wasn't kept: %+v, read at %v, unseenAt %v", d.threads[k], d.threadRead[k], s.unseenAt)
	}
}
