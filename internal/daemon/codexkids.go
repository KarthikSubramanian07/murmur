package daemon

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/title"
)

// Sessions another session started, where T3's database doesn't say so (#452). They are
// delegates as a T3 delegate_task child is (#188, threads.go), and go off the mesh the same way:
// offMeshLocked and parentOfLocked ask kids first. What differs is how one is recognised.
//
// A hook says so (#455 and the like): a hook request with Child set marks the session whose
// client's ID is the request's Native, and ParentNative, if given, is the client's ID of the
// session that started it. The daemon keeps what was said, by Native, so that a session whose MCP
// server registers after the hook spoke is a child from its first refresh.
//
// Codex says so in its state (#454): no hook of Murmur's can ask Codex for a thread's parent, but
// Codex's database records who started each thread (title.CodexLineage). Its source is a
// subagent's (one a Codex thread spawned, a review, a guardian), or the program that asked for it
// is one that runs Codex as another agent's subagents (title.CodexDrivers: Claude Code's
// codex-sub). That needs the thread's ID, which a Codex session's MCP server learns only from a
// call to one of Murmur's tools, and a thread a subagent tool drives often never calls one. So:
//
//   - The ID is taken from Codex's hook too, which runs at the thread's first prompt (hookNative).
//     That pairing is by process and can be wrong where a subagent's hook runs before its MCP
//     server registers: it pairs with the session that started the subagent. So an ID from a
//     hook counts only for a driven thread, not a spawned one, and only in a process that
//     another session's agent is above, which a person's Codex isn't.
//   - Where the hook is paired with no session (several in one Codex process), the thread it
//     reported still says something about the process (codexProc): a subagent tool starts a Codex
//     app server of its own, so every thread there is its. A process is marked driven, for good,
//     when a hook reports a driven thread from it and one of its ancestors is another session's
//     agent, which for codex-sub is the Claude Code that runs the plugin. The second condition
//     keeps a person's Codex out: its desktop app can open a thread codex-sub made, and nothing
//     above the desktop app is an agent. A session of a driven process is a child unless its own
//     thread was read and is a person's.
//
// A Codex session inside T3 is looked at here until its T3 thread is found: codex-sub passes
// T3's variables on to its Codex, so a thread it drives for a Claude Code session in T3 has
// T3's command line and never a T3 thread. Only a driven thread is marked there. A subagent
// that a Codex thread of T3's spawned is T3's to judge (#188): Tyler's rule (2026-11-12, #452)
// is that a thread shown in the user's list of sessions is on the mesh, and T3 knows which of
// its threads those are.
//
// The parent, for the refusal's pointer and for crediting commits: the session whose client's ID
// is the one the hook or Codex named, else the nearest session whose agent is an ancestor of the
// child's client process. With neither the child is off the mesh and its parent unnamed.
//
// Once a child, always: a session is never put back on the mesh, and stays a child while it is
// stopped with its inbox kept. Nothing is saved, so a daemon that restarts learns it again.

// kid is a session another started: the client's ID of the session that started it, if anything
// named one, and its client process's ancestors, nearest first, without the process itself.
type kid struct {
	parentNative string
	ancestors    []int
}

// codexThread is a Codex thread, by Codex's state directory and its ID.
type codexThread struct{ dir, thread string }

// maxKidNatives is how many hook-declared children are remembered by their client's ID.
const maxKidNatives = 2000

// codexDir is the Codex state directory a route's session uses: its own, as its MCP server gave
// it, else the daemon's CODEX_HOME, else Codex's default (#88; see readTitle).
func codexDir(r proto.Route) string {
	dir := r.CodexHome
	if !filepath.IsAbs(dir) {
		if dir = os.Getenv("CODEX_HOME"); !filepath.IsAbs(dir) {
			dir = title.DefaultCodexDir()
		}
	}
	return dir
}

// noteHookChildLocked keeps what a hook said of the session whose client's ID is native: that
// another session started it, and that one's client's ID, "" if the hook doesn't know. The
// sessions here with that ID are children at once. A parent once named isn't forgotten by a
// later request that names none. Caller holds mu.
func (d *Daemon) noteHookChildLocked(native, parentNative string) {
	if native == "" {
		return
	}
	if d.kidNative == nil {
		d.kidNative = map[string]string{}
	}
	if old, known := d.kidNative[native]; !known && len(d.kidNative) >= maxKidNatives {
		clear(d.kidNative) // a daemon that old relearns the live ones from their next hook
	} else if known && parentNative == "" {
		parentNative = old
	}
	d.kidNative[native] = parentNative
	marked := false
	for _, s := range d.nativeSessionsLocked(native) {
		marked = d.markKidLocked(s.id, parentNative) || marked
	}
	if marked {
		d.kickPresence()
		d.kickPush()
	}
}

// markKidLocked makes session id a child, and says whether it wasn't one. Caller holds mu.
func (d *Daemon) markKidLocked(id, parentNative string) bool {
	if d.kids == nil {
		d.kids = map[string]kid{}
	}
	k, was := d.kids[id]
	if parentNative != "" {
		k.parentNative = parentNative
	}
	if s := d.sessions[id]; s != nil && k.ancestors == nil {
		if anc := d.procAnc[s.clientPID]; len(anc) > 1 {
			k.ancestors = anc[1:]
		}
	}
	d.kids[id] = k
	if !was {
		d.log.Printf("session %s was started by another session, so it is off the mesh", bus.Addr(d.device, id))
	}
	return !was
}

// noteCodexHookLocked keeps the thread a Codex hook reported from the Codex process that ran it:
// the nearest of the hook's ancestors that is a Codex session's client. Caller holds mu.
func (d *Daemon) noteCodexHookLocked(ancestors []int, thread string) {
	if thread == "" {
		return
	}
	for _, pid := range ancestors {
		for _, s := range d.sessions {
			if pid != 0 && s.clientPID == pid && isAgent(s.agent, "codex") {
				if d.codexProc == nil {
					d.codexProc = map[int]string{}
				}
				d.codexProc[pid] = thread
				return
			}
		}
	}
}

// refreshKids finds the sessions another session started: those a hook named, whose MCP server
// has since registered, and the Codex sessions whose thread Codex's state says is another's. It
// says whether any session became a child. A lineage read once stands, since Codex doesn't
// change who started a thread, and so do a process's ancestors. Codex's database and the
// system's processes are read outside the lock.
func (d *Daemon) refreshKids(ctx context.Context) (changed bool) {
	type cand struct {
		id       string
		pid      int
		own, any codexThread // the session's own thread, and one a hook reported from its process
		fromHook bool        // own is hookNative: the hook's pairing, not the MCP server's word
		inT3     bool        // the session has T3's command line, and no T3 thread yet
	}
	d.mu.Lock()
	var cands []cand
	need := map[codexThread]bool{}
	needAnc := map[int]bool{}
	pids := map[int]bool{}
	for _, s := range d.sortedSessionsLocked() {
		if !s.dropped.IsZero() {
			continue // its process may be gone, and its number another's by now
		}
		pids[s.clientPID] = true
		if s.clientPID != 0 && d.procAnc[s.clientPID] == nil {
			needAnc[s.clientPID] = true
		}
		if !isAgent(s.agent, "codex") || s.route.T3Thread != "" {
			continue
		}
		c := cand{id: s.id, pid: s.clientPID, inT3: s.route.T3CLI != ""}
		dir := codexDir(s.route)
		if id := cmp.Or(s.route.Native, s.hookNative); id != "" {
			c.own, c.fromHook = codexThread{dir, id}, s.route.Native == ""
		}
		if id := d.codexProc[s.clientPID]; id != "" && !d.codexDriven[s.clientPID] {
			c.any = codexThread{dir, id}
		}
		for _, k := range []codexThread{c.own, c.any} {
			if _, known := d.codexLin[k]; !known && k.thread != "" {
				need[k] = true
			}
		}
		cands = append(cands, c)
	}
	d.mu.Unlock()

	read := map[codexThread]title.Lineage{}
	failed := map[codexThread]error{}
	for k := range need {
		if ctx.Err() != nil {
			return false
		}
		l, err := d.opt.CodexLineage(ctx, k.dir, k.thread)
		switch {
		case err == nil:
			read[k] = l
		case !errors.Is(err, title.ErrNoThread): // Codex writes the row a moment after the thread starts, or never
			failed[k] = err
		}
	}
	anc := map[int][]int{}
	for pid := range needAnc {
		// Once for each client process: on Windows each step is a snapshot of every process.
		if a := d.opt.Ancestors(pid, 16); len(a) > 0 {
			anc[pid] = a
		} else {
			anc[pid] = []int{pid}
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.codexLin == nil {
		d.codexLin, d.codexWarned = map[codexThread]title.Lineage{}, map[codexThread]bool{}
		d.codexDriven, d.procAnc = map[int]bool{}, map[int][]int{}
	}
	for k, l := range read {
		d.codexLin[k] = l
		delete(d.codexWarned, k)
	}
	for pid, a := range anc {
		d.procAnc[pid] = a
	}
	for pid := range d.codexDriven {
		if !pids[pid] {
			delete(d.codexDriven, pid)
		}
	}
	for pid := range d.procAnc {
		if !pids[pid] {
			delete(d.procAnc, pid)
		}
	}
	for pid := range d.codexProc {
		if !pids[pid] {
			delete(d.codexProc, pid)
		}
	}

	used := map[codexThread]bool{}
	for _, c := range cands {
		used[c.own], used[c.any] = true, true
		if l, ok := d.codexLin[c.any]; ok && l.Driven && d.agentAboveLocked(c.pid) {
			d.codexDriven[c.pid] = true
		}
	}
	for _, c := range cands {
		own, known := d.codexLin[c.own]
		switch {
		case known && own.Driven && (!c.fromHook || d.agentAboveLocked(c.pid)),
			known && own.Subagent && !c.fromHook && !c.inT3:
			changed = d.markKidLocked(c.id, own.Parent) || changed
		case !known && d.codexDriven[c.pid]:
			changed = d.markKidLocked(c.id, "") || changed
		}
		for _, k := range []codexThread{c.own, c.any} {
			if err := failed[k]; err != nil && !d.codexWarned[k] {
				d.codexWarned[k] = true
				d.log.Printf("can't read who started the Codex thread of session %s (%v); it stays on the mesh until that is read",
					bus.Addr(d.device, c.id), err)
			}
		}
	}
	for k := range d.codexLin {
		if !used[k] {
			delete(d.codexLin, k)
		}
	}
	for k := range d.codexWarned {
		if !used[k] {
			delete(d.codexWarned, k)
		}
	}
	// A session a hook named before its MCP server registered.
	for _, s := range d.sessions {
		if parent, ok := d.kidNative[s.route.Native]; ok && s.route.Native != "" && s.claudeSession == "" {
			changed = d.markKidLocked(s.id, parent) || changed
		}
	}
	for id, k := range d.kids {
		s := d.sessions[id]
		if s == nil && !d.receivesLocked(id) {
			delete(d.kids, id)
			continue
		}
		if s != nil && k.ancestors == nil {
			if a := d.procAnc[s.clientPID]; len(a) > 1 {
				k.ancestors = a[1:]
				d.kids[id] = k
			}
		}
	}
	return changed
}

// agentAboveLocked says one of the ancestors of client process pid is the client of a session
// here: an agent started the process, or started what did. Caller holds mu.
func (d *Daemon) agentAboveLocked(pid int) bool {
	anc := d.procAnc[pid]
	if len(anc) < 2 {
		return false
	}
	for _, s := range d.sessions {
		if s.clientPID != 0 && s.clientPID != pid && s.dropped.IsZero() && slices.Contains(anc[1:], s.clientPID) {
			return true
		}
	}
	return false
}

// kidParentLocked is the session here that the child id answers to, "" if none is found: the one
// whose client's ID is the one named as its parent, else the one whose agent is the nearest
// ancestor of its client process. Caller holds mu.
func (d *Daemon) kidParentLocked(id string, k kid) string {
	live := func(s *session) bool { return s.id != id && s.dropped.IsZero() }
	if k.parentNative != "" {
		for _, s := range d.sortedSessionsLocked() {
			if live(s) && cmp.Or(s.route.Native, s.hookNative) == k.parentNative {
				return s.id
			}
		}
	}
	for _, pid := range k.ancestors {
		for _, s := range d.sortedSessionsLocked() {
			if _, kid := d.kids[s.id]; live(s) && !kid && s.clientPID == pid {
				return s.id
			}
		}
	}
	return ""
}
