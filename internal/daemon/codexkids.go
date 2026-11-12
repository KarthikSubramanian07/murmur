package daemon

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/title"
)

// Codex threads another agent or thread started, outside T3 Code (#454). They are delegates as a
// T3 delegate_task child is (#188, threads.go), and go off the mesh the same way; what differs
// is how one is recognised, since T3's database knows nothing of them.
//
// Codex's own state says who started a thread (title.CodexLineage): its source is a subagent's
// (one a Codex thread spawned, a review, a guardian), or the program that asked for it is one
// that runs Codex as another agent's subagents (title.CodexDrivers: Claude Code's codex-sub).
// That needs the thread's ID, which a Codex session's MCP server learns only from a call to one
// of Murmur's tools. A thread a subagent tool drives often never calls one, so the ID is taken
// from Codex's hook too, which runs at the thread's first prompt: the session's own, where the
// hook could be paired with it (hookNative), and otherwise any thread the hook reported from the
// session's Codex process (codexProc). The second is enough for a driven thread and for nothing
// else: such a program starts a Codex app server of its own, so every thread in that process is
// its. A person's Codex spawning subagents runs them in its own process, beside its own thread.
//
// The parent, for the refusal's pointer and for crediting commits: for a thread Codex spawned,
// the session in the thread that spawned it; else the nearest session whose agent is among the
// ancestors of the child's Codex process, which for codex-sub is the Claude Code session that
// runs the plugin. With neither the child is off the mesh and its parent unnamed.
//
// Until a hook or a tool call gives a thread ID the session is on the mesh like any other, as a
// T3 delegate is until its thread is read. A session whose Codex has no hook (it was never
// trusted, #225) and that never calls a Murmur tool stays there.

// codexKid is a session in a Codex thread another started: the thread that spawned it, if Codex
// names one, and its Codex process's ancestors, nearest first, without the process itself.
type codexKid struct {
	parentThread string
	ancestors    []int
}

// codexThread is a Codex thread, by Codex's state directory and its ID.
type codexThread struct{ dir, thread string }

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

// noteCodexHookLocked keeps the thread a Codex hook reported from process pid. Caller holds mu.
func (d *Daemon) noteCodexHookLocked(pid int, thread string) {
	if pid == 0 || thread == "" {
		return
	}
	if d.codexProc == nil {
		d.codexProc = map[int]string{}
	}
	d.codexProc[pid] = thread
}

// refreshCodexKids reads, for every live Codex session outside T3, who started its thread, and
// keeps which of them are another's. It says whether that changed for any session. A lineage
// read once stands: Codex doesn't change who started a thread. The database is read outside the
// lock.
func (d *Daemon) refreshCodexKids(ctx context.Context) (changed bool) {
	type cand struct {
		id, who  string
		pid      int
		own, any codexThread // the session's own thread, and one a hook reported from its process
	}
	d.mu.Lock()
	var cands []cand
	need := map[codexThread]bool{}
	pids := map[int]bool{}
	for _, s := range d.sortedSessionsLocked() {
		if s.route.T3CLI != "" || !isAgent(s.agent, "codex") {
			continue
		}
		c := cand{id: s.id, who: bus.Addr(d.device, s.id), pid: s.clientPID}
		dir := codexDir(s.route)
		if id := cmp.Or(s.route.Native, s.hookNative); id != "" {
			c.own = codexThread{dir, id}
		}
		if id := d.codexProc[s.clientPID]; id != "" && s.clientPID != 0 {
			c.any = codexThread{dir, id}
		}
		for _, k := range []codexThread{c.own, c.any} {
			if _, known := d.codexLin[k]; !known && k.thread != "" {
				need[k] = true
			}
		}
		pids[s.clientPID] = true
		cands = append(cands, c)
	}
	for pid := range d.codexProc {
		if !pids[pid] {
			delete(d.codexProc, pid)
		}
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
		case !errors.Is(err, title.ErrNoThread): // Codex writes the row a moment after the thread starts
			failed[k] = err
		}
	}

	d.mu.Lock()
	if d.codexLin == nil {
		d.codexLin, d.codexKids, d.codexWarned = map[codexThread]title.Lineage{}, map[string]codexKid{}, map[codexThread]bool{}
	}
	for k, l := range read {
		d.codexLin[k] = l
		delete(d.codexWarned, k)
	}
	kids := map[string]codexKid{}
	used := map[codexThread]bool{}
	var fresh []cand
	for _, c := range cands {
		used[c.own], used[c.any] = true, true
		own, any := d.codexLin[c.own], d.codexLin[c.any]
		if !own.Child() && !any.Driven {
			continue
		}
		old, was := d.codexKids[c.id]
		kids[c.id] = codexKid{parentThread: own.Parent, ancestors: old.ancestors}
		if !was {
			fresh = append(fresh, c)
			d.log.Printf("session %s is in a Codex thread another agent or thread started, so it is off the mesh", c.who)
		}
	}
	changed = len(fresh) > 0 || len(kids) != len(d.codexKids)
	d.codexKids = kids
	for k := range d.codexLin {
		if !used[k] {
			delete(d.codexLin, k)
			delete(d.codexWarned, k)
		}
	}
	for _, c := range cands {
		for _, k := range []codexThread{c.own, c.any} {
			if err := failed[k]; err != nil && !d.codexWarned[k] {
				d.codexWarned[k] = true
				d.log.Printf("can't read who started the Codex thread of session %s (%v); it stays on the mesh until that is read", c.who, err)
			}
		}
	}
	d.mu.Unlock()

	// A process's ancestors are looked up once, when its session is first found to be another's:
	// on Windows each step is a snapshot of every process.
	for _, c := range fresh {
		if c.pid == 0 {
			continue
		}
		anc := d.opt.Ancestors(c.pid, 16)
		if len(anc) < 2 {
			continue
		}
		d.mu.Lock()
		if k, ok := d.codexKids[c.id]; ok {
			k.ancestors = anc[1:]
			d.codexKids[c.id] = k
		}
		d.mu.Unlock()
	}
	return changed
}

// codexParentLocked is the session here that the Codex child id answers to, "" if none is found:
// the one in the thread that spawned it, else the one whose agent is the nearest ancestor of its
// Codex process. Caller holds mu.
func (d *Daemon) codexParentLocked(id string, k codexKid) string {
	live := func(s *session) bool { return s.id != id && s.dropped.IsZero() }
	if k.parentThread != "" {
		for _, s := range d.sortedSessionsLocked() {
			if live(s) && s.route.T3CLI == "" && isAgent(s.agent, "codex") && cmp.Or(s.route.Native, s.hookNative) == k.parentThread {
				return s.id
			}
		}
	}
	for _, pid := range k.ancestors {
		for _, s := range d.sortedSessionsLocked() {
			if _, kid := d.codexKids[s.id]; live(s) && !kid && s.clientPID == pid {
				return s.id
			}
		}
	}
	return ""
}
