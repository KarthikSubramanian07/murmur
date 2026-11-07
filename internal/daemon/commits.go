package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/a-Fig/murmur/internal/attrib"
	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/gitx"
	"github.com/a-Fig/murmur/internal/proto"
)

// Which commits are a session's, and when it was last active (#21). docs/VISION.md has the
// reasons.
//
// Git doesn't record which session made a commit, so the daemon watches for them: the Claude Code
// hook runs before and after every tool call that could commit, and the commits that appear in the
// worktree between the two are the session's (package attrib decides which those are). A human or
// another session committing in the same worktree during that call is credited too, and that is
// accepted. A call that never ends, because it was cancelled or the hook failed, leaves a snapshot
// behind; those are bounded, and expire.

const (
	// maxBrackets caps the snapshots of calls in flight, and bracketTTL says how long one waits
	// for its call to end. A tool call can run for a long time (a subagent, a build), so an hour; an
	// agent runs a handful of calls at once, so a hundred is far more than needed. Past the cap the
	// oldest goes.
	maxBrackets = 128
	bracketTTL  = time.Hour
	// maxCommits caps the commits kept for a session; the oldest go first.
	maxCommits = 100
	// defaultSnapshotTimeout bounds the git a snapshot runs, a little under what the hook waits for
	// it (Options.SnapshotTimeout).
	defaultSnapshotTimeout = 4 * time.Second
)

// bracket is the start of a tool call: the worktree the hook ran in and what it looked like then.
// The call's end is judged in this worktree, and also in the one the hook is in by then if that is
// another worktree of the clone (see judgeEnd).
type bracket struct {
	cwd    string
	before attrib.Ref
	at     time.Time
}

// commitRec is a commit credited to a session. It is local to this device. At is when the call that
// made it ended, not the commit's own date. Branch is empty for a commit made on a detached HEAD:
// it is kept, and shown, but names no branch, so it adds none to the session's list.
type commitRec struct {
	SHA     string    `json:"sha"`
	Branch  string    `json:"branch,omitempty"`
	Subject string    `json:"subject,omitempty"`
	At      time.Time `json:"at"`
}

// snapshot is attrib.Head, which a test can slow down.
var snapshot = attrib.Head

// snapshotLimit is how long a snapshot may take: the option, or its default where it isn't set.
func (d *Daemon) snapshotLimit() time.Duration {
	if d.opt.SnapshotTimeout > 0 {
		return d.opt.SnapshotTimeout
	}
	return defaultSnapshotTimeout
}

func bracketKey(req proto.Request) string { return req.ClaudeSession + "/" + req.ToolUseID }

// sameClone says whether gitDir, the common dir the hook found for its working directory, is this
// daemon's own: a call in another repository, or in none, is not this daemon's to watch.
func (d *Daemon) sameClone(gitDir string) bool {
	if gitDir == "" {
		return false
	}
	if filepath.Clean(gitDir) == filepath.Clean(d.opt.GitDir) {
		return true
	}
	a, err := os.Stat(gitDir)
	if err != nil {
		return false
	}
	b, err := os.Stat(d.opt.GitDir)
	return err == nil && os.SameFile(a, b)
}

// touchHookLocked finds the sessions a hook request speaks for, as notices does, and notes that
// they are active. Caller holds mu.
func (d *Daemon) touchHookLocked(req proto.Request) []*session {
	sessions := d.hookSessionsLocked(req.ClaudeSession, req.ClientPID)
	now := time.Now()
	for _, s := range sessions {
		d.touchLocked(s, now)
	}
	return sessions
}

// toolStart snapshots the worktree the hook ran in, if the hook speaks for a session here. The
// hook waits for the reply and the tool call starts when it has it, so the snapshot has to be taken
// before replying: a commit made before it would count as old history. It replies OK whatever
// happens, as the hook has nothing to do with an error.
//
// It first waits for the calls that have ended and whose worktrees haven't been snapshotted again
// yet (toolEnd answers before it snapshots). This call could otherwise switch branches or reset
// before that snapshot, which would then miss what the earlier call committed, or credit it to the
// wrong branch. The wait and the snapshot share one limit, under the hook's.
func (d *Daemon) toolStart(ctx context.Context, req proto.Request) proto.Response {
	d.mu.Lock()
	sessions := d.touchHookLocked(req)
	unsnapped := slices.Clone(d.unsnapped)
	d.mu.Unlock()
	if len(sessions) == 0 || req.ToolUseID == "" || req.Cwd == "" || !d.sameClone(req.GitDir) {
		return proto.Response{OK: true}
	}
	ctx, cancel := context.WithTimeout(ctx, d.snapshotLimit())
	defer cancel()
	for _, ch := range unsnapped {
		select {
		case <-ch:
		case <-ctx.Done():
		}
	}
	before, err := attrib.Head(ctx, req.Cwd)
	if err != nil {
		d.snapshotFailed("snapshotting before a tool call", err)
		return proto.Response{OK: true}
	}
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, b := range d.brackets {
		if now.Sub(b.at) > bracketTTL {
			delete(d.brackets, key)
		}
	}
	for len(d.brackets) >= maxBrackets {
		oldest := ""
		for key, b := range d.brackets {
			if oldest == "" || b.at.Before(d.brackets[oldest].at) {
				oldest = key
			}
		}
		delete(d.brackets, oldest)
	}
	d.brackets[bracketKey(req)] = &bracket{cwd: req.Cwd, before: before, at: now}
	return proto.Response{OK: true}
}

// toolEnd closes the call's bracket, and answers at once: the hook, and with it the agent and the
// notices the hook brings, would otherwise wait for git. It then snapshots the worktree again in
// the background, before any call that starts later is let start, and credits the sessions the
// hook speaks for with the commits that appeared since. A call with no bracket (its start was skipped, expired, or came before a daemon restart)
// credits nothing.
func (d *Daemon) toolEnd(ctx context.Context, req proto.Request) proto.Response {
	key := bracketKey(req)
	ended := time.Now()
	d.mu.Lock()
	sessions := d.touchHookLocked(req)
	b := d.brackets[key]
	delete(d.brackets, key)
	if b == nil || len(sessions) == 0 {
		d.mu.Unlock()
		return proto.Response{OK: true}
	}
	d.ending++
	unsnapped := make(chan struct{})
	d.unsnapped = append(d.unsnapped, unsnapped)
	d.mu.Unlock()
	var once sync.Once
	snapped := func() {
		once.Do(func() {
			d.mu.Lock()
			d.unsnapped = slices.DeleteFunc(d.unsnapped, func(ch chan struct{}) bool { return ch == unsnapped })
			d.mu.Unlock()
			close(unsnapped)
		})
	}
	moved := ""
	if req.Cwd != "" && req.Cwd != b.cwd && d.sameClone(req.GitDir) {
		moved = req.Cwd
	}
	// Not cut short by shutdown, which waits for bg: a call that ended is still credited.
	ctx = context.WithoutCancel(ctx)
	d.bg.Add(1) // from a connection's goroutine, which bg counts, so never while bg drains to zero
	go func() {
		defer d.bg.Done()
		defer func() {
			d.mu.Lock()
			d.ending--
			d.mu.Unlock()
		}()
		defer snapped()
		d.judgeEnd(ctx, b, moved, ended, sessions, snapped)
	}()
	return proto.Response{OK: true}
}

// settleEnds waits until no ended call is still being judged, or for as long as judging one may
// take, so that what lookup says includes every call that has ended.
func (d *Daemon) settleEnds() {
	deadline := time.Now().Add(d.snapshotLimit() + time.Second)
	for {
		d.mu.Lock()
		n := d.ending
		d.mu.Unlock()
		if n == 0 || time.Now().After(deadline) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// judgeEnd snapshots the worktree a call started in, calls snapped, and credits the sessions with
// the commits made since its start, as of when the call ended. A call that ended with the hook in another worktree of the
// clone, moved, is judged there too, against the same start: the start's tips are the whole
// clone's, so they keep that worktree's history out as well as this one's, but for what only a
// detached HEAD there holds (#60).
func (d *Daemon) judgeEnd(ctx context.Context, b *bracket, moved string, ended time.Time, sessions []*session, snapped func()) {
	ctx, cancel := context.WithTimeout(ctx, d.snapshotLimit())
	defer cancel()
	worktrees := []string{b.cwd}
	if moved != "" && !sameWorktree(b.cwd, moved) {
		worktrees = append(worktrees, moved)
	}
	afters := make([]attrib.Ref, len(worktrees))
	errs := make([]error, len(worktrees))
	for i, wt := range worktrees {
		afters[i], errs[i] = snapshot(ctx, wt)
	}
	snapped()
	commits, ok := d.commitsSince(ctx, b.cwd, b.before, afters[0], errs[0])
	if len(worktrees) > 1 {
		more, _ := d.commitsSince(ctx, moved, b.before, afters[1], errs[1])
		for _, c := range more {
			if !slices.ContainsFunc(commits, func(o attrib.Commit) bool { return o.SHA == c.SHA }) {
				commits = append(commits, c)
			}
		}
		ok = ok || len(more) > 0
	}
	if !ok || len(commits) == 0 {
		return
	}
	d.mu.Lock()
	published := d.creditLocked(sessions, commits, ended)
	d.saveLocked()
	d.mu.Unlock()
	if published {
		d.kickPresence()
	}
}

// commitsSince returns the commits made in worktree between the snapshots before and after, which
// failed if err isn't nil. ok is false if git failed, which is logged.
func (d *Daemon) commitsSince(ctx context.Context, worktree string, before, after attrib.Ref, err error) ([]attrib.Commit, bool) {
	if err != nil {
		d.snapshotFailed("snapshotting after a tool call", err)
		return nil, false
	}
	commits, err := attrib.NewCommits(ctx, worktree, before, after)
	if err != nil {
		d.snapshotFailed("finding the commits a tool call made", err)
		return nil, false
	}
	return commits, true
}

// sameWorktree says whether two directories are in one worktree, where that can be told without
// git; where it can't, they are taken to be in two, which costs only a second look.
func sameWorktree(a, b string) bool {
	ga, _, okA := gitx.Discover(a)
	gb, _, okB := gitx.Discover(b)
	return okA && okB && filepath.Clean(ga) == filepath.Clean(gb)
}

// snapshotFailed logs a snapshot that couldn't be taken, at most once a minute: it would otherwise
// fill the log with a line per tool call, for a worktree that went away or a repository git can't
// read.
func (d *Daemon) snapshotFailed(what string, err error) {
	d.mu.Lock()
	quiet := time.Since(d.snapLogged) < time.Minute
	if !quiet {
		d.snapLogged = time.Now()
	}
	d.mu.Unlock()
	if !quiet {
		d.log.Printf("%s: %v", what, err)
	}
}

// creditLocked records commits as made by sessions, each at most once per session: calls that
// overlap, in parallel or because one is a subagent running others, see the same commits. at is
// when the call ended, and the records stay in that order: calls are judged in the background, and
// one that ended first can be judged last. It says whether any session's list of branches changed,
// which presence then shows. Caller holds mu.
func (d *Daemon) creditLocked(sessions []*session, commits []attrib.Commit, at time.Time) (branchesChanged bool) {
	for _, s := range sessions {
		before := d.committedLocked(s.id)
		recs := d.st.Commits[s.id]
		for _, c := range commits {
			if !slices.ContainsFunc(recs, func(r commitRec) bool { return r.SHA == c.SHA }) {
				i := len(recs)
				for i > 0 && recs[i-1].At.After(at) {
					i--
				}
				recs = slices.Insert(recs, i, commitRec{SHA: c.SHA, Branch: c.Branch, Subject: bus.OneLine(c.Subject, 120), At: at})
			}
		}
		if len(recs) > maxCommits {
			recs = slices.Clone(recs[len(recs)-maxCommits:])
		}
		d.st.Commits[s.id] = recs
		if !slices.Equal(before, d.committedLocked(s.id)) {
			branchesChanged = true
		}
	}
	return branchesChanged
}

// committedLocked lists the branches session id has committed to, most recent commit first, at
// most bus.MaxCommitted: what its presence shows. Caller holds mu.
func (d *Daemon) committedLocked(id string) []string {
	recs := d.st.Commits[id]
	var out []string
	for i := len(recs) - 1; i >= 0 && len(out) < bus.MaxCommitted; i-- {
		if b := recs[i].Branch; b != "" && !slices.Contains(out, b) {
			out = append(out, b)
		}
	}
	return out
}

// peerActive is when a session on another device was last active, on this machine's clock, or zero
// if its presence doesn't say. Presence holds the writer's clock at both the time of writing and the
// last activity; their difference is right whatever the clocks say, and it is taken back from when
// this daemon saw the presence change.
func peerActive(p *peer, s bus.SessionInfo) time.Time {
	if s.LastActive.IsZero() || p.pres.At.IsZero() {
		return time.Time{}
	}
	return p.lastChange.Add(-max(p.pres.At.Sub(s.LastActive), 0))
}
