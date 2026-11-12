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

// Sessions another session started are off the mesh outside T3's own delegates too (#452): Codex
// threads, by Codex's state (#454), and sessions a hook names. End to end, with stand-ins for
// Codex's state and for the system's processes.

// fakeCodex is the stand-in for Codex's state: who started each thread, as the test sets it. A
// thread it doesn't have is one Codex hasn't written, or never writes.
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

// agentSession registers a session of agent whose MCP server's parent process is pid, with the
// route r. A Claude Code session's conversation is claude.
func (d *device) agentSession(id, agent string, pid int, claude string, r proto.Route) (string, net.Conn) {
	d.t.Helper()
	conn, resp, err := d.c.Hello(context.Background(), proto.Hello{ID: id, Agent: agent, ClientPID: pid, ClaudeSession: claude, Route: r})
	if err != nil {
		d.t.Fatalf("hello %s: %v", id, err)
	}
	d.t.Cleanup(func() { conn.Close() })
	return resp.Self, conn
}

// codexIn is the route of a Codex session outside T3 whose MCP server knows its thread.
func codexIn(thread string) proto.Route { return proto.Route{Native: thread, CodexCLI: "codex"} }

// A Codex thread that a subagent tool drives (codex-sub, by the name it gave Codex) leaves who,
// takes no messages and can't send; the refusals name the session whose agent runs the tool,
// found among the ancestors of the thread's Codex process. The tool passes T3's variables on, so
// under a Claude Code session in T3 the thread's route has T3's command line and never a T3
// thread. A Codex thread a person started, in another process, stays.
func TestCodexThreadOfASubagentToolIsReachedThroughItsParent(t *testing.T) {
	remote := newRemote(t)
	exe, _ := useFakeT3(t)
	codex := &fakeCodex{
		lin:   map[string]title.Lineage{"th-person": {}},
		above: map[int][]int{300: {300, 200, 100}, 400: {400, 1}},
	}
	a, b := newDevice(t, remote), newCodexDevice(t, remote, codex)
	alice, _ := a.session("aaaaaa", "")
	boss, _ := b.agentSession("bbbbbb", "claude-code", 100, "claude-boss", proto.Route{})
	helper, _ := b.agentSession("cccccc", "codex in T3", 300, "", proto.Route{T3CLI: exe, Native: "th-helper"})
	person, _ := b.agentSession("dddddd", "codex", 400, "", codexIn("th-person"))
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
// Codex process; a driven thread there, under another session's agent, says the process is the
// tool's, and its sessions with no thread of their own read are children. That stands when a
// later hook reports a thread Codex's state doesn't have. A session of the process whose own
// thread is a person's stays, and so does a process no agent is above (a person's Codex that
// opened one of the tool's threads).
func TestCodexThreadsAreMarkedByAHookFromTheirProcess(t *testing.T) {
	remote := newRemote(t)
	codex := &fakeCodex{
		lin:   map[string]title.Lineage{"th-seen": {Driven: true}, "th-own": {}},
		above: map[int][]int{300: {300, 200, 100}, 500: {500, 1}},
	}
	b := newCodexDevice(t, remote, codex)
	boss, _ := b.agentSession("bbbbbb", "claude-code", 100, "claude-boss", proto.Route{})
	one, _ := b.agentSession("cccccc", "codex", 300, "", proto.Route{CodexCLI: "codex"})
	two, _ := b.agentSession("dddddd", "codex", 300, "", proto.Route{CodexCLI: "codex"})
	own, _ := b.agentSession("eeeeee", "codex", 300, "", codexIn("th-own"))
	desk, _ := b.agentSession("ffffff", "codex", 500, "", proto.Route{CodexCLI: "codex"})
	desk2, _ := b.agentSession("gggggg", "codex", 500, "", proto.Route{CodexCLI: "codex"})
	all := []string{boss, one, two, own, desk, desk2}
	eventually(t, "the sessions to be listed", func() bool {
		for _, s := range all {
			if !b.listsSession(s) {
				return false
			}
		}
		return true
	})

	// The hook runs under a shell under Codex. Several sessions share each Codex, so the hook
	// is paired with none of them.
	b.do(proto.Request{Op: proto.OpNotices, Agent: "codex", ClaudeSession: "th-seen", Ancestors: []int{901, 900, 300, 200, 100}})
	b.do(proto.Request{Op: proto.OpNotices, Agent: "codex", ClaudeSession: "th-seen", Ancestors: []int{911, 910, 500, 1}})
	eventually(t, "both unread sessions of the driven process to leave who", func() bool {
		return !b.listsSession(one) && !b.listsSession(two)
	})
	b.do(proto.Request{Op: proto.OpNotices, Agent: "codex", ClaudeSession: "th-never-written", Ancestors: []int{902, 900, 300, 200, 100}})
	time.Sleep(300 * time.Millisecond) // several reads of the threads, at the tests' pace
	for name, s := range map[string]string{"the Claude Code session": boss, "a session whose own thread is a person's": own,
		"a session of a Codex no agent started": desk, "the other session of that Codex": desk2} {
		if !b.listsSession(s) {
			t.Errorf("%s left who", name)
		}
	}
	if b.listsSession(one) || b.listsSession(two) {
		t.Error("a child came back to who after a hook reported a thread Codex's state doesn't have")
	}
}

// A subagent Codex itself spawned runs in its parent's process, so only its own thread marks it,
// and the thread that spawned it is its parent. A thread ID that came from a hook's pairing
// doesn't: the hook of a subagent can be paired with the session that spawned it.
func TestCodexSubagentIsReachedThroughTheThreadThatSpawnedIt(t *testing.T) {
	remote := newRemote(t)
	exe, _ := useFakeT3(t)
	codex := &fakeCodex{
		lin:   map[string]title.Lineage{"th-main": {}, "th-spawned": {Subagent: true, Parent: "th-x"}, "th-driven-below": {Driven: true}},
		above: map[int][]int{300: {300, 1}, 600: {600, 1}, 650: {650, 1}},
	}
	b := newCodexDevice(t, remote, codex)
	main, _ := b.agentSession("bbbbbb", "codex", 300, "", codexIn("th-main"))
	kid, _ := b.agentSession("cccccc", "codex", 300, "", codexIn("th-kid"))
	lone, _ := b.agentSession("dddddd", "codex", 600, "", proto.Route{CodexCLI: "codex"})
	// In T3, whether a spawned thread is a delegate is T3's to say, once its thread is found.
	inT3, _ := b.agentSession("eeeeee", "codex in T3", 300, "", proto.Route{T3CLI: exe, Native: "th-spawned"})
	mainName, kidName := b.named(main), b.named(kid)
	eventually(t, "the sessions to be listed", func() bool {
		return b.listsSession(main) && b.listsSession(kid) && b.listsSession(lone) && b.listsSession(inT3)
	})

	// The lone session of its process is paired with the hook of a subagent it spawned.
	b.do(proto.Request{Op: proto.OpNotices, Agent: "codex", ClaudeSession: "th-spawned", Ancestors: []int{921, 920, 600, 1}})
	// Or of a driven thread started below it (it ran an agent that runs codex-sub), whose hook
	// came before that thread's own MCP server registered.
	solo, _ := b.agentSession("ffffff", "codex", 650, "", proto.Route{CodexCLI: "codex"})
	eventually(t, "the session to be listed", func() bool { return b.listsSession(solo) })
	b.do(proto.Request{Op: proto.OpNotices, Agent: "codex", ClaudeSession: "th-driven-below", Ancestors: []int{931, 930, 650, 1}})
	codex.set("th-kid", title.Lineage{Subagent: true, Parent: "th-main"})
	eventually(t, "the subagent to leave who", func() bool { return !b.listsSession(kid) })
	if !b.listsSession(main) {
		t.Error("the thread that spawned the subagent left who")
	}
	if !b.listsSession(lone) || !b.listsSession(solo) {
		t.Error("a session left who on the word of a hook's pairing")
	}
	if !b.listsSession(inT3) {
		t.Error("a spawned thread in T3 left who on Codex's word, before T3 said what it is")
	}
	if resp := b.trySend("bbbbbb", kidName, "to my subagent"); resp.OK || !strings.Contains(resp.Error, "Message "+mainName+" instead") {
		t.Errorf("a send to the subagent: ok %v, error %q", resp.OK, resp.Error)
	}
	if resp := b.trySend("cccccc", mainName, "from the subagent"); resp.OK || !strings.Contains(resp.Error, mainName) {
		t.Errorf("the subagent's own send: ok %v, error %q", resp.OK, resp.Error)
	}
}

// A hook that says its session is another's child (#455) takes it off the mesh, with the parent
// it names, whether the session's MCP server registered before the hook spoke or after.
func TestAHookNamesItsSessionAChild(t *testing.T) {
	remote := newRemote(t)
	b := newCodexDevice(t, remote, &fakeCodex{lin: map[string]title.Lineage{}, above: map[int][]int{}})
	main, _ := b.agentSession("bbbbbb", "opencode", 700, "", proto.Route{Native: "ses-main"})
	early, _ := b.agentSession("cccccc", "opencode", 700, "", proto.Route{Native: "ses-early"})
	mainName, earlyName := b.named(main), b.named(early)
	eventually(t, "the sessions to be listed", func() bool { return b.listsSession(main) && b.listsSession(early) })

	b.do(proto.Request{Op: proto.OpNotices, Native: "ses-early", Child: true, ParentNative: "ses-main"})
	b.do(proto.Request{Op: proto.OpNotices, Native: "ses-late", Child: true, ParentNative: "ses-main"})
	eventually(t, "the child to leave who", func() bool { return !b.listsSession(early) })
	if resp := b.trySend("bbbbbb", earlyName, "to my child"); resp.OK || !strings.Contains(resp.Error, "Message "+mainName+" instead") {
		t.Errorf("a send to the child: ok %v, error %q", resp.OK, resp.Error)
	}
	if resp := b.trySend("cccccc", mainName, "from the child"); resp.OK || !strings.Contains(resp.Error, mainName) {
		t.Errorf("the child's own send: ok %v, error %q", resp.OK, resp.Error)
	}
	// A request that doesn't say so changes nothing.
	b.do(proto.Request{Op: proto.OpNotices, Native: "ses-early"})

	late, _ := b.agentSession("dddddd", "opencode", 700, "", proto.Route{Native: "ses-late"})
	eventually(t, "a child named before it registered to be refused", func() bool {
		resp := b.trySend("dddddd", mainName, "from the late child")
		return !resp.OK && strings.Contains(resp.Error, mainName)
	})
	if b.listsSession(late) || b.listsSession(early) || !b.listsSession(main) {
		t.Errorf("who lists late %v, early %v, main %v; want only main", b.listsSession(late), b.listsSession(early), b.listsSession(main))
	}
}
