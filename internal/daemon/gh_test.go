package daemon_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/proto"
)

// The page looks pull requests and GitHub's events up with gh (internal/web), found on PATH. A
// test that polls the page would run the user's own gh, logged in to their account, in a test's
// clone, and on CI whatever gh the runner has (#43). So TestMain puts a stand-in first on PATH for
// the whole run: the test binary again, under the name gh, which answers every list with none and
// records what it was asked. Being the same for every test, it needs nothing set per test, and
// the tests stay parallel.

// fakeGHName is the stand-in's file name, less the extension Windows needs.
const fakeGHName = "gh"

// fakeGHCalls is the file in the stand-in's directory that it appends its arguments to.
const fakeGHCalls = "gh-calls.jsonl"

// fakeGHDir is where TestMain put the stand-in.
var fakeGHDir string

// useFakeGH puts the stand-in gh in dir, and dir first on PATH, for the daemons this process
// starts and the processes they start.
func useFakeGH(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	exe := filepath.Join(dir, fakeGHName)
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if err := placeTestBinary(exe); err != nil {
		return err
	}
	fakeGHDir = dir
	return os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// fakeGH is the stand-in gh, run from dir: it records its arguments and prints an empty list,
// which is what gh prints for `pr list` and `issue list` with --json when there are none.
func fakeGH(dir string) int {
	line, _ := json.Marshal(os.Args[1:])
	f, err := os.OpenFile(filepath.Join(dir, fakeGHCalls), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 3
	}
	f.Write(append(line, '\n'))
	f.Close()
	os.Stdout.WriteString("[]\n")
	return 0
}

// ghCalls is the arguments of every run of the stand-in so far, by any test.
func ghCalls(t *testing.T) [][]string {
	t.Helper()
	f, err := os.Open(filepath.Join(fakeGHDir, fakeGHCalls))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out [][]string
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var args []string
		if json.Unmarshal(sc.Bytes(), &args) == nil {
			out = append(out, args)
		}
	}
	return out
}

// The page's lookup of a session's pull request runs the stand-in, not the gh of whoever runs the
// tests.
func TestThePagesPullRequestLookupRunsTheStandInGH(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a := newDevice(t, remote)
	branch := "gh-stand-in-" + bus.NewID() // this test's own, as every test shares the stand-in
	wt := a.linkedTree("wt-gh", branch)
	conn, _, err := a.c.Hello(context.Background(), proto.Hello{ID: "aaaaaa", Agent: "test", Cwd: wt})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	p := a.page()
	eventually(t, "the stand-in gh to be asked about the session's branch", func() bool {
		p.state() // the first poll starts the lookup, in the background
		return slices.ContainsFunc(ghCalls(t), func(args []string) bool { return slices.Contains(args, "--head="+branch) })
	})
}
