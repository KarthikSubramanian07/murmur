package hook_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/client"
	"github.com/a-Fig/murmur/internal/hook"
	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/view"
)

// fakeDaemon serves the daemon's protocol for the clone at gitDir: answer gives the response line
// to each request, or "" to hold the connection open without one. It returns the requests so far.
func fakeDaemon(t *testing.T, gitDir string, answer func(proto.Request) string) func() []proto.Request {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { close(done); ln.Close() })
	var mu sync.Mutex
	var got []proto.Request
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadBytes('\n')
				if err != nil {
					return
				}
				var req proto.Request
				json.Unmarshal(line, &req)
				mu.Lock()
				got = append(got, req)
				mu.Unlock()
				if resp := answer(req); resp != "" {
					c.Write([]byte(resp + "\n"))
				} else {
					<-done
				}
			}()
		}
	}()
	dir := client.StateDir(gitDir)
	os.MkdirAll(dir, 0o700)
	b, _ := json.Marshal(proto.Info{Addr: ln.Addr().String(), Token: "tok", PID: os.Getpid(), Device: "fake-0001"})
	if err := os.WriteFile(filepath.Join(dir, "daemon.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return func() []proto.Request {
		mu.Lock()
		defer mu.Unlock()
		return append([]proto.Request(nil), got...)
	}
}

func okToAll(proto.Request) string { return `{"ok":true}` }

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TRACE2_EVENT=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "root")
	return repo
}

func run(event, cwd string) (string, time.Duration) {
	payload, _ := json.Marshal(map[string]string{"session_id": "claude-x", "hook_event_name": event,
		"cwd": cwd, "tool_name": "Bash", "tool_use_id": "toolu_1"})
	var out bytes.Buffer
	start := time.Now()
	hook.Run(bytes.NewReader(payload), &out)
	return out.String(), time.Since(start)
}

// Before a tool call the hook runs no git process of its own: it finds the clone from its files,
// as one process would add a tenth of a second or more to every call on Windows (#21). It finds
// the same clone from a linked worktree and from a subdirectory.
func TestPreToolUseRunsNoGit(t *testing.T) {
	repo := newRepo(t)
	linked := filepath.Join(t.TempDir(), "wt")
	git(t, repo, "worktree", "add", "-q", "-b", "side", linked)
	sub := filepath.Join(linked, "a", "b")
	os.MkdirAll(sub, 0o755)
	gitDir := filepath.Join(repo, ".git")
	requests := fakeDaemon(t, gitDir, okToAll)

	trace := filepath.Join(t.TempDir(), "trace.json")
	t.Setenv("GIT_TRACE2_EVENT", trace)
	for _, cwd := range []string{repo, linked, sub} {
		if out, _ := run("PreToolUse", cwd); out != "" {
			t.Errorf("PreToolUse printed %q", out)
		}
	}
	b, _ := os.ReadFile(trace)
	if n := strings.Count(string(b), `"event":"start"`); n != 0 {
		t.Errorf("three PreToolUse hooks ran %d git processes, want none", n)
	}
	want, err := os.Stat(gitDir)
	if err != nil {
		t.Fatal(err)
	}
	var starts int
	for _, r := range requests() {
		if r.Op == proto.OpToolStart {
			starts++
			// The same directory, not the same string: on macOS a linked worktree's .git file
			// names the clone under /private/var, where the temp dir's own path is /var, a link to
			// it. The daemon compares the two the same way (sameClone).
			if got, err := os.Stat(r.GitDir); err != nil || !os.SameFile(got, want) {
				t.Errorf("the hook in %s found the clone at %q, want %q", r.Cwd, r.GitDir, gitDir)
			}
		}
	}
	if starts != 3 {
		t.Errorf("the daemon was told of %d calls, want 3", starts)
	}
}

// A daemon that takes the hook's connection and never answers holds a tool call up for no more
// than BracketTimeout, and the hook prints nothing. (A daemon that isn't running at all is
// TestPreToolUseWithTheDaemonDownIsSilent, in the daemon's tests.)
func TestPreToolUseGivesUpOnAHungDaemon(t *testing.T) {
	repo := newRepo(t)
	requests := fakeDaemon(t, filepath.Join(repo, ".git"), func(r proto.Request) string {
		if r.Op == proto.OpPing {
			return `{"ok":true}`
		}
		return "" // hung
	})
	out, took := run("PreToolUse", repo)
	if out != "" {
		t.Errorf("PreToolUse printed %q", out)
	}
	if limit := hook.BracketTimeout + 2*time.Second; took > limit {
		t.Errorf("PreToolUse waited %v for a hung daemon, more than %v", took, limit)
	}
	var told bool
	for _, r := range requests() {
		told = told || r.Op == proto.OpToolStart
	}
	if !told {
		t.Error("the hook didn't tell the daemon about the call")
	}
}

// A daemon from before #21 can still be running when the hook is upgraded: it refuses the ends of
// calls as an unknown op. The notices after a tool call still arrive.
func TestNoticesFromADaemonThatDoesntBracket(t *testing.T) {
	repo := newRepo(t)
	fakeDaemon(t, filepath.Join(repo, ".git"), func(r proto.Request) string {
		switch r.Op {
		case proto.OpPing:
			return `{"ok":true}`
		case proto.OpNotices:
			return `{"ok":true,"self":"fake-0001/aaaaaa","messages":[{"id":"m1","from":"other-0001/bbbbbb","subject":"the old daemon's notice"}]}`
		}
		return `{"ok":false,"error":"unknown op ` + r.Op + `"}`
	})
	if out, _ := run("PreToolUse", repo); out != "" {
		t.Errorf("PreToolUse printed %q", out)
	}
	out, _ := run("PostToolUse", repo)
	if !strings.Contains(out, "the old daemon's notice") {
		t.Errorf("PostToolUse printed %q, want the notice", out)
	}
}

// With the page's whole-message choice, the hook's additional context is exactly the turn the
// message would otherwise become (#204).
func TestWholeMessageIsTheTurnText(t *testing.T) {
	repo := newRepo(t)
	msg := proto.MessageView{ID: "m1", From: "other-0001/bbbbbb", FromName: "quiet-otter", FromAgent: "test",
		To: "fake-0001/aaaaaa", Subject: "all of it", Body: "first line\nsecond line", Whole: true}
	fakeDaemon(t, filepath.Join(repo, ".git"), func(r proto.Request) string {
		if r.Op == proto.OpNotices {
			b, _ := json.Marshal(proto.Response{OK: true, Self: "fake-0001/aaaaaa", Messages: []proto.MessageView{msg}})
			return string(b)
		}
		return `{"ok":true}`
	})
	out, _ := run("PostToolUse", repo)
	var decoded struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("hook output %q: %v", out, err)
	}
	if got, want := decoded.HookSpecificOutput.AdditionalContext, view.Turn(msg); got != want {
		t.Errorf("additional context:\n got %q\nwant %q", got, want)
	}
}

// The transcript's path, which holds the session's title, goes to the daemon with the notices
// request (#66): the daemon cannot find it from the session ID alone, as the hook's payload names it.
func TestNoticesCarryTheTranscriptPath(t *testing.T) {
	repo := newRepo(t)
	requests := fakeDaemon(t, filepath.Join(repo, ".git"), okToAll)
	payload, _ := json.Marshal(map[string]string{"session_id": "claude-x", "hook_event_name": "UserPromptSubmit",
		"cwd": repo, "transcript_path": "/home/u/.claude/projects/p/claude-x.jsonl"})
	hook.Run(bytes.NewReader(payload), &bytes.Buffer{})
	for _, r := range requests() {
		if r.Op == proto.OpNotices {
			if r.Transcript != "/home/u/.claude/projects/p/claude-x.jsonl" {
				t.Errorf("the notices request carries transcript %q", r.Transcript)
			}
			return
		}
	}
	t.Errorf("the hook sent no notices request: %+v", requests())
}
