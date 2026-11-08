package here

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A command finds its session's server by the agent's process: CLAUDE_PID first, then its nearest
// ancestor that is some server's parent.
func TestCallerIsTheNearestAgent(t *testing.T) {
	a := Session{ServerPID: 10, ClientPID: 100}
	b := Session{ServerPID: 20, ClientPID: 200}
	all := []Session{a, b}
	if got := Caller(all, []int{7, 200, 100}, 0, ""); !slices.Equal(got, []Session{b}) {
		t.Errorf("by ancestors: %+v, want the nearest, %+v", got, b)
	}
	if got := Caller(all, []int{7, 200}, 100, ""); !slices.Equal(got, []Session{a}) {
		t.Errorf("CLAUDE_PID: %+v, want %+v", got, a)
	}
	if got := Caller(all, []int{7, 8}, 0, ""); got != nil {
		t.Errorf("no agent among the ancestors: %+v", got)
	}
}

// One process running several sessions gives them all back, unless Claude Code's session ID picks one.
func TestCallerOfASharedProcess(t *testing.T) {
	a := Session{ServerPID: 10, ClientPID: 100, ClaudeSession: "aaa"}
	b := Session{ServerPID: 11, ClientPID: 100, ClaudeSession: "bbb"}
	if got := Caller([]Session{a, b}, []int{100}, 0, ""); len(got) != 2 {
		t.Errorf("can't tell them apart, yet got %+v", got)
	}
	if got := Caller([]Session{a, b}, []int{100}, 0, "bbb"); !slices.Equal(got, []Session{b}) {
		t.Errorf("by session ID: %+v, want %+v", got, b)
	}
}

// A move reaches the server of that agent process, and only with the same Claude session: a new
// process that took a dead one's ID has another.
func TestAskedMatchesTheSession(t *testing.T) {
	dir := t.TempDir()
	s := Session{ServerPID: 10, ClientPID: 100, ClaudeSession: "aaa"}
	at := time.Now()
	if err := Ask(dir, s, "/work/repo", at); err != nil {
		t.Fatal(err)
	}
	m, ok := Asked(dir, 100, "aaa")
	if !ok || m.Dir != "/work/repo" || !m.At.Equal(at) {
		t.Errorf("Asked = %+v, %v", m, ok)
	}
	if _, ok := Asked(dir, 100, "zzz"); ok {
		t.Error("a move for another Claude session was taken")
	}
	if _, ok := Asked(dir, 101, "aaa"); ok {
		t.Error("a move for another process was taken")
	}
}

// Records of servers that exited, and moves for agents that exited, are cleared away.
func TestSessionsDropsTheDead(t *testing.T) {
	dir := t.TempDir()
	dead := exitedPID(t)
	live := Session{ServerPID: os.Getpid(), ClientPID: os.Getppid()}
	for _, s := range []Session{live, {ServerPID: dead, ClientPID: os.Getppid()}} {
		if err := Publish(dir, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := Ask(dir, Session{ClientPID: dead}, "/x", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := Sessions(dir)
	if err != nil || !slices.Equal(got, []Session{live}) {
		t.Fatalf("Sessions = %+v, %v; want only %+v", got, err, live)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(left) != 1 {
		t.Errorf("files left: %q, want only the live record", left)
	}
	Withdraw(dir, live.ServerPID)
	if got, _ := Sessions(dir); len(got) != 0 {
		t.Errorf("after Withdraw: %+v", got)
	}
}

func TestAncestorsStartWithTheParent(t *testing.T) {
	a := Ancestors()
	if len(a) == 0 || a[0] != os.Getppid() {
		t.Errorf("Ancestors = %v, want the parent %d first", a, os.Getppid())
	}
}

func exitedPID(t *testing.T) int {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}
