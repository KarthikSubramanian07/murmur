package daemon

import (
	"io"
	"log"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/proto"
)

// A Stop's continuation that no other hook shows went on stops counting as at work after
// ContinueHold, outside T3: its answer may have been lost, and the agent stopped. A hook of the
// session's in that time confirms it.
func TestALostContinuationLapses(t *testing.T) {
	t.Parallel()
	d := &Daemon{opt: Options{ContinueHold: time.Minute}, dir: t.TempDir(), log: log.New(io.Discard, "", 0), device: "here-0001", started: time.Now(),
		sessions: map[string]*session{}, threadRead: map[threadKey]time.Time{},
		st: state{Inbox: map[string][]*inMsg{}}}
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.newSessionLocked(&proto.Hello{ID: "bbbbbb", ClaudeSession: "claude-bob"}, "claude-code", "", "bbbbbb")
	s.hooked, s.stopSeen, s.stopContinues = true, true, true
	now := time.Now()
	s.busyAt, s.turnStart, s.continuedAt = now, now, now
	if !d.hookBusyLocked(s.id) {
		t.Fatal("not at work just after a continuation")
	}
	s.turnStart, s.continuedAt = now.Add(-time.Hour), now.Add(-d.opt.ContinueHold)
	if d.hookBusyLocked(s.id) {
		t.Fatal("still at work after continueHold with no hook since")
	}
	d.touchHookLocked(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-bob"})
	if !d.hookBusyLocked(s.id) || !s.continuedAt.IsZero() || s.continuations != 0 {
		t.Errorf("after a tool call's hook: busy %v, continuedAt %v, continuations %d",
			d.hookBusyLocked(s.id), s.continuedAt, s.continuations)
	}
	if s.turnStart.Before(now) {
		t.Error("a hook after a lapsed continuation doesn't start a new turn")
	}
}

// A lapse ends the turn only of a session outside T3, by its route: a T3 session whose thread
// hasn't been read (the reads failing) keeps its turn, which the database ends once a read works.
func TestALapseLeavesATurnInT3ToTheDatabase(t *testing.T) {
	t.Parallel()
	d := &Daemon{opt: Options{ContinueHold: time.Minute}, dir: t.TempDir(), log: log.New(io.Discard, "", 0), device: "here-0001",
		started: time.Now(), sessions: map[string]*session{}, threadRead: map[threadKey]time.Time{},
		st: state{Inbox: map[string][]*inMsg{}}, pushKick: make(chan struct{}, 1)}
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.newSessionLocked(&proto.Hello{ID: "bbbbbb", ClaudeSession: "claude-bob"}, "claude-code", "", "bbbbbb")
	s.route = proto.Route{T3CLI: "t3", T3Thread: "thread-bob"}
	s.hooked, s.stopSeen, s.stopContinues = true, true, true
	now := time.Now()
	s.busyAt, s.turnStart, s.continuedAt = now, now, now.Add(-d.opt.ContinueHold)
	d.endLapsedTurnsLocked(pageSettings{})
	if s.busyAt.IsZero() || !s.continuedAt.IsZero() {
		t.Errorf("after the lapse in T3: busyAt %v, continuedAt %v; want the turn kept, the mark gone", s.busyAt, s.continuedAt)
	}
	if !d.hookBusyLocked(s.id) {
		t.Error("the T3 session isn't at work after the lapse")
	}
}
