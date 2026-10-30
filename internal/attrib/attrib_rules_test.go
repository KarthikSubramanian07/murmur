package attrib_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/a-Fig/t4/internal/attrib"
)

// backdate makes a commit whose committer date is when, so that rule 3 treats it as made before
// a command that starts now. Commits made in a test are otherwise stamped within the slack.
func backdate(t *testing.T, dir, subject string, when time.Time) string {
	t.Helper()
	// Git accepts a raw "<unix seconds> <offset>" date; it stores whole seconds, as Unix() does.
	gitEnv(t, dir, []string{fmt.Sprintf("GIT_COMMITTER_DATE=@%d +0000", when.Unix())},
		"commit", "-q", "--allow-empty", "-m", subject)
	return git(t, dir, "rev-parse", "HEAD")
}

// clone copies src into a new directory and sets the identity in that clone's own config, since
// git spawns git for rebase and merge and those children should not depend on global config.
func clone(t *testing.T, src string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "clone")
	git(t, src, "clone", "-q", src, dst)
	git(t, dst, "config", "user.name", "Attribution Test")
	git(t, dst, "config", "user.email", "attrib@example.invalid")
	return dst
}

func TestPullRebaseCreditsOnlyRewrittenLocalCommits(t *testing.T) {
	up := repo(t)
	commit(t, up, "base")
	d := clone(t, up)
	commit(t, d, "local")
	backdate(t, up, "teammate", time.Now().Add(-time.Hour))
	before := head(t, d)
	git(t, d, "pull", "-q", "--rebase")
	after := head(t, d)
	if after.SHA == before.SHA {
		t.Fatal("pull --rebase did not move HEAD")
	}
	if got := git(t, d, "log", "-1", "--format=%s", "HEAD^"); got != "teammate" {
		t.Fatalf("parent of rewritten commit = %q, want the fetched teammate commit", got)
	}
	// The rewritten "local" is new; the fetched "teammate" was committed an hour before the command.
	wantCommits(t, d, before, after, attrib.Commit{SHA: after.SHA, Subject: "local", Branch: "main"})
}

func TestCommitAndPushCreditsCommit(t *testing.T) {
	bare := t.TempDir()
	git(t, bare, "init", "-q", "--bare", "-b", "main")
	d := repo(t)
	commit(t, d, "base")
	git(t, d, "remote", "add", "origin", bare)
	git(t, d, "push", "-q", "origin", "main")
	before := head(t, d)
	sha := commit(t, d, "pushed")
	// After the push, refs/remotes/origin/main reaches the new commit, but the snapshot predates it.
	git(t, d, "push", "-q", "origin", "main")
	wantCommits(t, d, before, head(t, d), attrib.Commit{SHA: sha, Subject: "pushed", Branch: "main"})
}

func TestMergeOfFetchedBranchCreditsOnlyMergeCommit(t *testing.T) {
	up := repo(t)
	commit(t, up, "base")
	d := clone(t, up)
	// The feature branch is created after the clone, so the clone does not already track it.
	git(t, up, "checkout", "-q", "-b", "feature")
	backdate(t, up, "feature work", time.Now().Add(-time.Hour))
	git(t, up, "checkout", "-q", "main")
	// Diverge locally so that the merge is a real merge commit rather than a fast-forward.
	commit(t, d, "local")
	before := head(t, d)
	git(t, d, "fetch", "-q", "origin", "feature")
	git(t, d, "merge", "-q", "--no-ff", "-m", "merge feature", "origin/feature")
	after := head(t, d)
	wantCommits(t, d, before, after, attrib.Commit{SHA: after.SHA, Subject: "merge feature", Branch: "main"})
}

func TestCommitterSlackBoundary(t *testing.T) {
	dir := repo(t)
	base := commit(t, dir, "base")
	start := time.Date(2000, 1, 1, 12, 0, 0, 500_000_000, time.UTC)
	// Built by hand with no tips, so only rule 3 is under test. The slack is 2 s before start,
	// which is 11:59:58.5, and the stored second is what counts: 11:59:57 is excluded and 11:59:59 is kept.
	before := attrib.Ref{SHA: base, Branch: "main", Time: start}
	backdate(t, dir, "too early", start.Add(-2600*time.Millisecond))
	within := backdate(t, dir, "within slack", start.Add(-1*time.Second))
	wantCommits(t, dir, before, head(t, dir), attrib.Commit{SHA: within, Subject: "within slack", Branch: "main"})
}

func TestCommitterSlackUpperBoundary(t *testing.T) {
	dir := repo(t)
	base := commit(t, dir, "base")
	start := time.Date(2000, 1, 1, 12, 0, 0, 500_000_000, time.UTC)
	end := start.Add(10 * time.Second)
	// Both Refs built by hand, so only the dates are under test. The slack ends 2 s after end,
	// at 12:00:12.5, and the stored second is what counts: 12:00:12 is kept and 12:00:13 is not.
	before := attrib.Ref{SHA: base, Branch: "main", Time: start}
	within := backdate(t, dir, "within slack", end.Add(1500*time.Millisecond))
	backdate(t, dir, "too late", end.Add(2600*time.Millisecond))
	after := attrib.Ref{SHA: git(t, dir, "rev-parse", "HEAD"), Branch: "main", Time: end}
	wantCommits(t, dir, before, after, attrib.Commit{SHA: within, Subject: "within slack", Branch: "main"})
}

func TestNewCommitsRejectsRefWithoutTime(t *testing.T) {
	dir := repo(t)
	sha := commit(t, dir, "root")
	if _, err := attrib.NewCommits(context.Background(), dir, attrib.Ref{SHA: sha, Branch: "main"}, head(t, dir)); err == nil {
		t.Error("NewCommits accepted a before Ref that Head did not read")
	}
	if _, err := attrib.NewCommits(context.Background(), dir, head(t, dir), attrib.Ref{SHA: sha, Branch: "main"}); err == nil {
		t.Error("NewCommits accepted an after Ref that Head did not read")
	}
}
