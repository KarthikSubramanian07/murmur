package bus_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/a-Fig/t4/internal/bus"
	"github.com/a-Fig/t4/internal/gitx"
)

// IsCommit answers only for what it can tell: a commit, or not one (a tree, an object that isn't
// here). A git that fails otherwise gives no answer, since callers build over what isn't a commit.
func TestIsCommit(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	ctx := context.Background()
	r := &bus.Repo{Git: gitx.Git{Dir: dir}, Name: "test", Email: "test@example.invalid"}
	commit, err := r.NewCommit(ctx, "", "a commit")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := r.EmptyTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		what, sha string
		want      bool
	}{{"a commit", commit, true}, {"a tree", tree, false}, {"a missing object", strings.Repeat("0", 39) + "1", false}} {
		if got, err := r.IsCommit(ctx, c.sha); err != nil || got != c.want {
			t.Errorf("%s: got %v, %v; want %v", c.what, got, err, c.want)
		}
	}
	broken := &bus.Repo{Git: gitx.Git{Dir: filepath.Join(dir, "nowhere")}}
	if got, err := broken.IsCommit(ctx, commit); err == nil {
		t.Errorf("a git that can't run answered %v", got)
	}
}

// One Repo serves all of a daemon's goroutines, and the first commits can come at once (the
// presence loop, a session's first event). Under -race this catches an unguarded cache.
func TestNewCommitConcurrently(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	r := &bus.Repo{Git: gitx.Git{Dir: dir}, Name: "test", Email: "test@example.invalid"}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if _, err := r.NewCommit(context.Background(), "", "x"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}
