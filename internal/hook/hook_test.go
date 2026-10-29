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

	"github.com/a-Fig/t4/internal/client"
	"github.com/a-Fig/t4/internal/hook"
	"github.com/a-Fig/t4/internal/proto"
)

// fakeDaemon answers every request with OK and records them, for the repository at dir.
func fakeDaemon(t *testing.T, gitDir string) func() []proto.Request {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
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
				c.Write([]byte(`{"ok":true}` + "\n"))
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

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TRACE2_EVENT=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// Before a tool call the hook runs no git process of its own: it finds the clone from its files,
// as one process would add a tenth of a second or more to every call on Windows (#21). It finds
// the same clone from a linked worktree and from a subdirectory.
func TestPreToolUseRunsNoGit(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "root")
	linked := filepath.Join(t.TempDir(), "wt")
	git(t, repo, "worktree", "add", "-q", "-b", "side", linked)
	sub := filepath.Join(linked, "a", "b")
	os.MkdirAll(sub, 0o755)
	gitDir := filepath.Join(repo, ".git")
	requests := fakeDaemon(t, gitDir)

	trace := filepath.Join(t.TempDir(), "trace.json")
	t.Setenv("GIT_TRACE2_EVENT", trace)
	for _, cwd := range []string{repo, linked, sub} {
		payload, _ := json.Marshal(map[string]string{"session_id": "claude-x", "hook_event_name": "PreToolUse",
			"cwd": cwd, "tool_name": "Bash", "tool_use_id": "toolu_1"})
		var out bytes.Buffer
		hook.Run(bytes.NewReader(payload), &out)
		if out.Len() != 0 {
			t.Errorf("PreToolUse printed %q", out.String())
		}
	}
	b, _ := os.ReadFile(trace)
	if n := strings.Count(string(b), `"event":"start"`); n != 0 {
		t.Errorf("three PreToolUse hooks ran %d git processes, want none", n)
	}
	var starts int
	for _, r := range requests() {
		if r.Op == proto.OpToolStart {
			starts++
			if r.GitDir != filepath.Clean(gitDir) {
				t.Errorf("the hook in %s found the clone at %q, want %q", r.Cwd, r.GitDir, gitDir)
			}
		}
	}
	if starts != 3 {
		t.Errorf("the daemon was told of %d calls, want 3", starts)
	}
}
