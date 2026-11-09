package web

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeGH puts a gh first on PATH that notes each run in its own folder and answers that there is
// no PR. On Windows it is a .cmd file, as a shim or wrapper can be, so it runs under cmd.exe.
func fakeGH(t *testing.T) (calls func() int) {
	t.Helper()
	bin := t.TempDir()
	if runtime.GOOS == "windows" {
		script := "@echo off\r\necho called>>\"%~dp0calls.txt\"\r\necho []\r\n"
		if err := os.WriteFile(filepath.Join(bin, "gh.cmd"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	} else {
		script := "#!/bin/sh\necho called >> \"$(dirname \"$0\")/calls.txt\"\necho '[]'\n"
		if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		b, _ := os.ReadFile(filepath.Join(bin, "calls.txt"))
		return strings.Count(string(b), "called")
	}
}

// A branch name comes from the bus, so whoever can push chooses it. One made to be read as
// commands by cmd.exe, which runs a gh that is a batch file, never reaches gh's command line.
func TestGHHostileBranch(t *testing.T) {
	calls := fakeGH(t)
	dir := t.TempDir() // where gh runs, and where a command smuggled in would write its file
	lookup := GH(dir)
	for _, branch := range []string{
		`x&echo,PWNED>pwned`,           // a legal git branch name: & , and > are all allowed
		`x"&echo,PWNED>pwned-quoted&"`, // breaking out of the quotes
		`x|echo,PWNED>pwned-pipe`,
		`feat/%USERNAME%`, // cmd.exe expands variables
		`feat/!PATH!`,
		`a^b`, `a<b`, `(a)`, `a;b`, `a,b`, `a=b`, `a'b`, "a`b", `a$b`,
	} {
		if pr, err := lookup(context.Background(), branch); pr != nil || err != nil {
			t.Errorf("%q: %v, %v", branch, pr, err)
		}
	}
	if n := calls(); n != 0 {
		t.Errorf("gh ran %d times for branches that should never reach it", n)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "pwned*")); len(left) != 0 {
		t.Errorf("a branch name ran a command, which wrote %v", left)
	}

	// The fake gh does run for an ordinary branch, so the counts above mean something.
	if _, err := lookup(context.Background(), "feature/page-22"); err != nil {
		t.Fatalf("an ordinary branch: %v", err)
	}
	if n := calls(); n != 1 {
		t.Errorf("gh ran %d times for an ordinary branch, want 1", n)
	}
}

func TestLookupable(t *testing.T) {
	for _, b := range []string{"main", "web-page", "feature/page-22", "tyler/fix_names.v2", "v1.2+build",
		"user@fix#22", "fonctionnalité/été", "功能"} {
		if !lookupable(b) {
			t.Errorf("%q refused", b)
		}
	}
	for _, b := range []string{"", "-x", "--head", "a b", "a\tb", "a\nb", "x&calc", `a"b`, "a|b", "a<b",
		"a>b", "a^b", "a%b", "a!b", "a(b", "a)b", "a;b", "a,b", "a=b", "a'b", "a`b", "a$b", "a\\b",
		"a‮b", strings.Repeat("a", maxBranch+1)} {
		if lookupable(b) {
			t.Errorf("%q accepted", b)
		}
	}
}

// Closing the page's server ends the lookups it started (each runs gh in the clone) before it
// returns, and a request after that starts none: nothing the page started outlives the daemon.
func TestCloseEndsLookups(t *testing.T) {
	started := make(chan string, 4)
	var ended atomic.Int32
	s := NewWithPRs(newFake(), testToken, func(ctx context.Context, branch string) (*PR, error) {
		started <- branch
		<-ctx.Done() // a gh that hangs until it is stopped
		ended.Add(1)
		return nil, ctx.Err()
	})
	s.prs.get([]string{"feat-1"})
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("no lookup started")
	}
	s.Close()
	if ended.Load() != 1 {
		t.Errorf("Close returned with %d of 1 lookups ended", ended.Load())
	}
	s.prs.get([]string{"feat-2"})
	time.Sleep(100 * time.Millisecond)
	select {
	case b := <-started:
		t.Errorf("a lookup for %s started after Close", b)
	default:
	}
}
