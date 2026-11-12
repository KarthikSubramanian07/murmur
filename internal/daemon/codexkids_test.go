package daemon_test

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/title"
)

// Codex threads another agent or thread started are off the mesh outside T3 too (#454), end to
// end, with stand-ins for Codex's state and for the system's processes.

// fakeCodex is the stand-in for Codex's state: who started each thread, as the test sets it. A
// thread it doesn't have is one Codex hasn't written yet.
type fakeCodex struct {
	mu  sync.Mutex
	lin map[string]title.Lineage
	// above is each process's ancestors, nearest first, the process itself first.
	above map[int][]int
}

func (f *fakeCodex) set(thread string, l title.Lineage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lin[thread] = l
}

func (f *fakeCodex) read(_ context.Context, _, thread string) (title.Lineage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.lin[thread]
	if !ok {
		return title.Lineage{}, title.ErrNoThread
	}
	return l, nil
}

func (f *fakeCodex) ancestors(pid, _ int) []int { return f.above[pid] }

// newCodexDevice is newDevice whose Codex state and processes are codex.
func newCodexDevice(t *testing.T, remote string, codex *fakeCodex) *device {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	git(t, filepath.Dir(dir), "clone", "-q", "--no-local", remote, dir)
	d := &device{t: t, gitDir: filepath.Join(dir, ".git"), stale: 3 * time.Second, poll: 100 * time.Millisecond,
		codexLineage: codex.read, ancestors: codex.ancestors}
	d.start()
	return d
}

// agentSession registers a session of agent whose MCP server's parent process is pid, in the
// client's conversation native ("" while its server hasn't learned it).
func (d *device) agentSession(id, agent string, pid int, native string) (string, net.Conn) {
	d.t.Helper()
	h := proto.Hello{ID: id, Agent: agent, ClientPID: pid}
	if agent == "codex" {
		h.Route = proto.Route{Native: native, CodexCLI: "codex"}
	} else {
		h.ClaudeSession = native
	}
	conn, resp, err := d.c.Hello(context.Background(), h)
	if err != nil {
		d.t.Fatalf("hello %s: %v", id, err)
	}
	d.t.Cleanup(func() { conn.Close() })
	return resp.Self, conn
}

// A Codex thread that a subagent tool drives (codex-sub, by the name it gave Codex) leaves who,
// takes no messages and can't send; the refusals name the session whose agent runs the tool,
// found among the ancestors of the thread's Codex process. A Codex thread a person started, in
// another process, stays.
func TestCodexThreadOfASubagentToolIsReachedThroughItsParent(t *testing.T) {
	remote := newRemote(t)
	codex := &fakeCodex{
		lin:   map[string]title.Lineage{"th-person": {}},
		above: map[int][]int{300: {300, 200, 100}, 400: {400, 1}},
	}
	a, b := newDevice(t, remote), newCodexDevice(t, remote, codex)
	alice, _ := a.session("aaaaaa", "")
	boss, _ := b.agentSession("bbbbbb", "claude-code", 100, "claude-boss")
	helper, _ := b.agentSession("cccccc", "codex", 300, "th-helper")
	person, _ := b.agentSession("dddddd", "codex", 400, "th-person")
	bossName, helperName := b.named(boss), b.named(helper)
	a.nameIs(boss, bossName)
	a.nameIs(helper, helperName)
	eventually(t, "presence", func() bool { return b.seesSession(alice) && a.seesSession(person) })

	codex.set("th-helper", title.Lineage{Driven: true})
	eventually(t, "the driven thread to leave both devices' who", func() bool {
		return a.seesSession(boss) && !a.listsSession(helper) && !b.listsSession(helper)
	})
	if !a.seesSession(person) || !b.listsSession(person) {
		t.Error("a Codex thread a person started left who")
	}
	if resp := a.trySend("aaaaaa", helperName, "for the helper"); resp.OK || !strings.Contains(resp.Error, "delegate") {
		t.Errorf("a send to the driven thread from another device: ok %v, error %q", resp.OK, resp.Error)
	}
	if resp := b.trySend("bbbbbb", helperName, "local"); resp.OK || !strings.Contains(resp.Error, "Message "+bossName+" instead") {
		t.Errorf("a send to the driven thread on its device: ok %v, error %q", resp.OK, resp.Error)
	}
	if resp := b.trySend("cccccc", alice, "from the helper"); resp.OK || !strings.Contains(resp.Error, bossName) {
		t.Errorf("the driven thread's own send: ok %v, error %q", resp.OK, resp.Error)
	}
	if resp := b.trySend("dddddd", alice, "from the person's thread"); !resp.OK {
		t.Errorf("a send from a person's Codex thread: %q", resp.Error)
	}
}

// A thread a subagent tool drives often never calls a Murmur tool, so its MCP server never
// learns its thread. Codex's hook, at the thread's first prompt, reports a thread from the same
// Codex process, and a driven thread there says every session of that process is the tool's:
// the tool runs a Codex of its own.
func TestCodexThreadsAreMarkedByAHookFromTheirProcess(t *testing.T) {
	remote := newRemote(t)
	codex := &fakeCodex{lin: map[string]title.Lineage{"th-seen": {Driven: true}}, above: map[int][]int{}}
	b := newCodexDevice(t, remote, codex)
	one, _ := b.agentSession("cccccc", "codex", 300, "")
	two, _ := b.agentSession("dddddd", "codex", 300, "")
	other, _ := b.agentSession("eeeeee", "codex", 400, "")
	eventually(t, "the sessions to be listed", func() bool {
		return b.listsSession(one) && b.listsSession(two) && b.listsSession(other)
	})

	// The hook runs under a shell under Codex: two sessions share that Codex, so the hook is
	// paired with neither.
	b.do(proto.Request{Op: proto.OpNotices, Agent: "codex", ClaudeSession: "th-seen", Ancestors: []int{901, 900, 300}})
	eventually(t, "both sessions of the process to leave who", func() bool {
		return !b.listsSession(one) && !b.listsSession(two)
	})
	if !b.listsSession(other) {
		t.Error("a session of another Codex process left who")
	}
}

// A subagent Codex itself spawned runs in its parent's process, so only its own thread marks it,
// and the thread that spawned it is its parent.
func TestCodexSubagentIsReachedThroughTheThreadThatSpawnedIt(t *testing.T) {
	remote := newRemote(t)
	codex := &fakeCodex{lin: map[string]title.Lineage{"th-main": {}}, above: map[int][]int{300: {300, 1}}}
	b := newCodexDevice(t, remote, codex)
	main, _ := b.agentSession("bbbbbb", "codex", 300, "th-main")
	kid, _ := b.agentSession("cccccc", "codex", 300, "th-kid")
	mainName, kidName := b.named(main), b.named(kid)
	eventually(t, "the sessions to be listed", func() bool { return b.listsSession(main) && b.listsSession(kid) })

	codex.set("th-kid", title.Lineage{Subagent: true, Parent: "th-main"})
	eventually(t, "the subagent to leave who", func() bool { return !b.listsSession(kid) })
	if !b.listsSession(main) {
		t.Error("the thread that spawned the subagent left who")
	}
	if resp := b.trySend("bbbbbb", kidName, "to my subagent"); resp.OK || !strings.Contains(resp.Error, "Message "+mainName+" instead") {
		t.Errorf("a send to the subagent: ok %v, error %q", resp.OK, resp.Error)
	}
	if resp := b.trySend("cccccc", mainName, "from the subagent"); resp.OK || !strings.Contains(resp.Error, mainName) {
		t.Errorf("the subagent's own send: ok %v, error %q", resp.OK, resp.Error)
	}
}
