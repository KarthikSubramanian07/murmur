package attrib_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/attrib"
)

func hid_g(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=T", "-c", "user.email=t@example.invalid", "-c", "init.defaultBranch=main",
		"-c", "commit.gpgsign=false", "-c", "core.autocrlf=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".nogit"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func hid_commit(t *testing.T, dir, subject string) {
	t.Helper()
	f := filepath.Join(dir, "f.txt")
	b, _ := os.ReadFile(f)
	os.WriteFile(f, append(b, []byte(subject+"\n")...), 0o644)
	hid_g(t, dir, "add", "f.txt")
	hid_g(t, dir, "commit", "-q", "-m", subject)
}

// hid_commitOld makes a commit dated an hour ago: one that existed well before the command.
// Attribution judges by committer date, so a setup commit made in the same second as the
// snapshot would be indistinguishable from one the command made.
func hid_commitOld(t *testing.T, dir, subject string) {
	t.Helper()
	old := time.Now().Add(-time.Hour).Format(time.RFC3339)
	t.Setenv("GIT_COMMITTER_DATE", old)
	t.Setenv("GIT_AUTHOR_DATE", old)
	hid_commit(t, dir, subject)
	os.Unsetenv("GIT_COMMITTER_DATE")
	os.Unsetenv("GIT_AUTHOR_DATE")
}

func hid_repo(t *testing.T) string {
	d := t.TempDir()
	hid_g(t, d, "init", "-q")
	return d
}

func hid_run(t *testing.T, dir string, action func()) []string {
	t.Helper()
	ctx := context.Background()
	before, err := attrib.Head(ctx, dir)
	if err != nil {
		t.Fatalf("Head before: %v", err)
	}
	action()
	after, err := attrib.Head(ctx, dir)
	if err != nil {
		t.Fatalf("Head after: %v", err)
	}
	cs, err := attrib.NewCommits(ctx, dir, before, after)
	if err != nil {
		t.Fatalf("NewCommits: %v", err)
	}
	var subs []string
	for _, c := range cs {
		subs = append(subs, c.Subject)
	}
	return subs
}

func hid_want(t *testing.T, got []string, w ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(w, ",") {
		t.Errorf("got %q, want %q", got, w)
	}
}

func TestHiddenOrdinary(t *testing.T) {
	d := hid_repo(t)
	hid_commit(t, d, "base")
	hid_want(t, hid_run(t, d, func() { hid_commit(t, d, "a"); hid_commit(t, d, "b"); hid_commit(t, d, "c") }), "a", "b", "c")
}

func TestHiddenUnborn(t *testing.T) {
	d := hid_repo(t)
	hid_want(t, hid_run(t, d, func() { hid_commit(t, d, "first") }), "first")
}

func TestHiddenSwitchToDivergedBranch(t *testing.T) {
	d := hid_repo(t)
	hid_commit(t, d, "base")
	hid_g(t, d, "checkout", "-q", "-b", "other")
	hid_commit(t, d, "old-other")
	hid_g(t, d, "checkout", "-q", "main")
	hid_want(t, hid_run(t, d, func() { hid_g(t, d, "checkout", "-q", "other") }))
}

func TestHiddenSwitchToDivergedBranchAndCommit(t *testing.T) {
	d := hid_repo(t)
	hid_commit(t, d, "base")
	hid_g(t, d, "checkout", "-q", "-b", "other")
	hid_commit(t, d, "old-other")
	hid_g(t, d, "checkout", "-q", "main")
	hid_want(t, hid_run(t, d, func() { hid_g(t, d, "checkout", "-q", "other"); hid_commit(t, d, "new") }), "new")
}

func TestHiddenMergeExistingBranch(t *testing.T) {
	d := hid_repo(t)
	hid_commit(t, d, "base")
	hid_g(t, d, "checkout", "-q", "-b", "feature")
	os.WriteFile(filepath.Join(d, "g.txt"), []byte("x"), 0o644)
	hid_g(t, d, "add", "g.txt")
	hid_g(t, d, "commit", "-q", "-m", "old-feature")
	hid_g(t, d, "checkout", "-q", "main")
	hid_commit(t, d, "main-2")
	hid_want(t, hid_run(t, d, func() { hid_g(t, d, "merge", "-q", "--no-ff", "-m", "merge", "feature") }), "merge")
}

func TestHiddenFastForwardExistingBranch(t *testing.T) {
	d := hid_repo(t)
	hid_commit(t, d, "base")
	hid_g(t, d, "checkout", "-q", "-b", "feature")
	hid_commit(t, d, "old-feature")
	hid_g(t, d, "checkout", "-q", "main")
	hid_want(t, hid_run(t, d, func() { hid_g(t, d, "merge", "-q", "--ff-only", "feature") }))
}

func TestHiddenPullFromRemote(t *testing.T) {
	up := hid_repo(t)
	hid_commit(t, up, "base")
	d := t.TempDir()
	hid_g(t, d, "clone", "-q", up, ".")
	hid_commitOld(t, up, "teammate")
	hid_want(t, hid_run(t, d, func() { hid_g(t, d, "pull", "-q", "--ff-only") }))
}

func TestHiddenAmend(t *testing.T) {
	d := hid_repo(t)
	hid_commit(t, d, "base")
	hid_commit(t, d, "draft")
	hid_want(t, hid_run(t, d, func() { hid_g(t, d, "commit", "-q", "--amend", "-m", "fixed") }), "fixed")
}

func TestHiddenReset(t *testing.T) {
	d := hid_repo(t)
	hid_commit(t, d, "base")
	hid_commit(t, d, "x")
	hid_want(t, hid_run(t, d, func() { hid_g(t, d, "reset", "-q", "--hard", "HEAD~1") }))
}

func TestHiddenCherryPick(t *testing.T) {
	d := hid_repo(t)
	hid_commit(t, d, "base")
	hid_g(t, d, "checkout", "-q", "-b", "other")
	hid_commitOld(t, d, "pick-me")
	hid_g(t, d, "checkout", "-q", "main")
	hid_want(t, hid_run(t, d, func() { hid_g(t, d, "cherry-pick", "other") }), "pick-me")
}

func TestHiddenRebaseOntoExisting(t *testing.T) {
	d := hid_repo(t)
	hid_commit(t, d, "base")
	hid_g(t, d, "checkout", "-q", "-b", "feature")
	os.WriteFile(filepath.Join(d, "k.txt"), []byte("x"), 0o644)
	hid_g(t, d, "add", "k.txt")
	hid_g(t, d, "commit", "-q", "-m", "feat")
	hid_g(t, d, "checkout", "-q", "main")
	hid_commit(t, d, "main-2")
	hid_g(t, d, "checkout", "-q", "feature")
	// The rewritten "feat" is new; "main-2" already existed on main.
	hid_want(t, hid_run(t, d, func() { hid_g(t, d, "rebase", "-q", "main") }), "feat")
}

func TestHiddenLinkedWorktree(t *testing.T) {
	d := hid_repo(t)
	hid_commit(t, d, "base")
	wt := filepath.Join(t.TempDir(), "wt")
	hid_g(t, d, "worktree", "add", "-q", "-b", "side", wt)
	mainBefore, _ := attrib.Head(context.Background(), d)
	hid_want(t, hid_run(t, wt, func() { hid_commit(t, wt, "in-wt") }), "in-wt")
	mainAfter, _ := attrib.Head(context.Background(), d)
	if mainBefore.SHA != mainAfter.SHA || mainAfter.Branch != "main" {
		t.Errorf("main checkout HEAD changed or wrong: %+v -> %+v", mainBefore, mainAfter)
	}
	h, _ := attrib.Head(context.Background(), wt)
	if h.Branch != "side" {
		t.Errorf("worktree branch = %q, want side", h.Branch)
	}
}

func TestHiddenDetached(t *testing.T) {
	d := hid_repo(t)
	hid_commit(t, d, "base")
	hid_g(t, d, "checkout", "-q", "--detach")
	hid_want(t, hid_run(t, d, func() { hid_commit(t, d, "detached") }), "detached")
	h, _ := attrib.Head(context.Background(), d)
	if h.Branch != "" {
		t.Errorf("detached branch = %q", h.Branch)
	}
}

func TestHiddenNotARepo(t *testing.T) {
	if _, err := attrib.Head(context.Background(), t.TempDir()); err == nil {
		t.Errorf("Head outside a repo: no error")
	}
}
