package daemon

import (
	"context"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/a-Fig/t4/internal/attrib"
	"github.com/a-Fig/t4/internal/proto"
)

// The snapshots of tool calls in flight are bounded: a call whose end never comes (it was
// cancelled, or its hook died) must not leave one behind for good.

func bracketDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "--initial-branch=main"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	d := &Daemon{opt: Options{GitDir: filepath.Join(dir, ".git"), StoppedInbox: time.Hour}, dir: t.TempDir(),
		log: log.New(io.Discard, "", 0), device: "here-0001", started: time.Now(), sessions: map[string]*session{},
		brackets: map[string]*bracket{},
		st:       state{Inbox: map[string][]*inMsg{}, Stopped: map[string]time.Time{}, Commits: map[string][]commitRec{}}}
	d.mu.Lock()
	d.newSessionLocked(&proto.Hello{ID: "aaaaaa", ClaudeSession: "claude-x", Cwd: dir}, "claude-code", "", "aaaaaa")
	d.mu.Unlock()
	return d, dir
}

func (d *Daemon) startReq(dir, toolUse string) proto.Request {
	return proto.Request{Op: proto.OpToolStart, ClaudeSession: "claude-x", ToolUseID: toolUse, Cwd: dir, GitDir: d.opt.GitDir}
}

func TestStartedCallsAreCappedAndExpire(t *testing.T) {
	d, dir := bracketDaemon(t)
	now := time.Now()
	d.mu.Lock()
	for i := range maxBrackets {
		// The most recently started has the highest i; "old-0" is the oldest.
		d.brackets["claude-x/old-"+strconv.Itoa(i)] = &bracket{cwd: dir, before: attrib.Ref{}, at: now.Add(-time.Duration(maxBrackets-i) * time.Second)}
	}
	d.brackets["claude-x/ancient"] = &bracket{cwd: dir, at: now.Add(-bracketTTL - time.Minute)}
	d.mu.Unlock()

	d.toolStart(context.Background(), d.startReq(dir, "new"))

	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.brackets["claude-x/ancient"]; ok {
		t.Errorf("a snapshot older than %v was kept", bracketTTL)
	}
	if _, ok := d.brackets["claude-x/new"]; !ok {
		t.Fatal("the new call has no snapshot")
	}
	if len(d.brackets) > maxBrackets {
		t.Errorf("%d snapshots held, the cap is %d", len(d.brackets), maxBrackets)
	}
	if _, ok := d.brackets["claude-x/old-0"]; ok {
		t.Error("the oldest snapshot was kept over the new one")
	}
	if _, ok := d.brackets["claude-x/old-1"]; !ok {
		t.Error("a snapshot was dropped that was not the oldest")
	}
}

// What the hook sends that the daemon can't pair or place leaves nothing behind.
func TestStartsThatCannotBePairedAreNotKept(t *testing.T) {
	d, dir := bracketDaemon(t)
	for name, req := range map[string]proto.Request{
		"no tool_use_id":   {Op: proto.OpToolStart, ClaudeSession: "claude-x", Cwd: dir, GitDir: d.opt.GitDir},
		"no cwd":           {Op: proto.OpToolStart, ClaudeSession: "claude-x", ToolUseID: "t", GitDir: d.opt.GitDir},
		"another clone":    {Op: proto.OpToolStart, ClaudeSession: "claude-x", ToolUseID: "t", Cwd: dir, GitDir: t.TempDir()},
		"no clone":         {Op: proto.OpToolStart, ClaudeSession: "claude-x", ToolUseID: "t", Cwd: dir},
		"no session":       {Op: proto.OpToolStart, ClaudeSession: "claude-y", ToolUseID: "t", Cwd: dir, GitDir: d.opt.GitDir},
		"a dead directory": {Op: proto.OpToolStart, ClaudeSession: "claude-x", ToolUseID: "t", Cwd: filepath.Join(dir, "gone"), GitDir: d.opt.GitDir},
	} {
		if resp := d.toolStart(context.Background(), req); !resp.OK {
			t.Errorf("%s: the hook got an error: %q", name, resp.Error)
		}
		d.mu.Lock()
		n := len(d.brackets)
		d.mu.Unlock()
		if n != 0 {
			t.Errorf("%s: %d snapshots kept", name, n)
		}
	}
}

// A state.json written before commits were kept has no "commits" key; loading it must leave a map
// to credit into, as a write to a nil map would panic the daemon on the first commit.
func TestStateFromBeforeCommitsLoads(t *testing.T) {
	dir := t.TempDir()
	old := `{"processed":{},"inbox":{},"stopped":{},"usedNames":{"quiet-otter":"s1"}}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{dir: dir, log: log.New(io.Discard, "", 0)}
	if err := d.loadState(); err != nil {
		t.Fatal(err)
	}
	if d.st.Commits == nil || d.st.Used["quiet-otter"] != "s1" {
		t.Fatalf("loaded %+v", d.st)
	}
	s := &session{id: "s1"}
	d.mu.Lock()
	d.creditLocked([]*session{s}, []attrib.Commit{{SHA: "abc", Branch: "main"}}, time.Now())
	d.mu.Unlock()
	if got := d.st.Commits["s1"]; len(got) != 1 || got[0].SHA != "abc" {
		t.Errorf("credited %+v", got)
	}
}
