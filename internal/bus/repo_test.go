package bus_test

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/a-Fig/t4/internal/brand"
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

// A leased push is a compare-and-swap: it lands only if the remote ref holds what it expects. Of
// two racing on the same expectation, exactly one lands, and the other is told ErrRejected,
// whether git refused it before sending or the server did once the other had landed.
func TestLeasePush(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	ctx := context.Background()
	ref := brand.RefRoot + "lease"
	repos := make([]*bus.Repo, 2)
	for i := range repos {
		dir := filepath.Join(root, fmt.Sprintf("local%d.git", i))
		for _, d := range []string{remote, dir} {
			if out, err := exec.Command("git", "init", "-q", "--bare", d).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v: %s", err, out)
			}
		}
		repos[i] = &bus.Repo{Git: gitx.Git{Dir: dir}, Remote: remote, Name: "test", Email: "test@example.invalid"}
	}
	commit := func(r *bus.Repo, msg string) string {
		t.Helper()
		sha, err := r.NewCommit(ctx, "", msg)
		if err != nil {
			t.Fatal(err)
		}
		return sha
	}

	// Expecting no ref: lands when there is none, and is refused once there is.
	tip := commit(repos[0], "first")
	if err := repos[0].Push(ctx, tip, ref, bus.Lease, ""); err != nil {
		t.Fatalf("first push: %v", err)
	}
	if err := repos[1].Push(ctx, commit(repos[1], "also first"), ref, bus.Lease, ""); !errors.Is(err, bus.ErrRejected) {
		t.Fatalf("push expecting no ref, over one: %v", err)
	}

	for round := range 5 {
		shas, errs := make([]string, len(repos)), make([]error, len(repos))
		for i, r := range repos {
			shas[i] = commit(r, fmt.Sprintf("round %d, pusher %d", round, i))
		}
		var wg sync.WaitGroup
		for i, r := range repos {
			wg.Go(func() { errs[i] = r.Push(ctx, shas[i], ref, bus.Lease, tip) })
		}
		wg.Wait()
		won := -1
		for i, err := range errs {
			switch {
			case err == nil && won >= 0:
				t.Fatalf("round %d: both pushes landed", round)
			case err == nil:
				won = i
			case !errors.Is(err, bus.ErrRejected):
				t.Fatalf("round %d, pusher %d: %v", round, i, err)
			}
		}
		if won < 0 {
			t.Fatalf("round %d: neither push landed", round)
		}
		if now, err := repos[0].RemoteRef(ctx, ref); err != nil || now != shas[won] {
			t.Fatalf("round %d: the remote holds %s (%v), want the winner's %s", round, now, err, shas[won])
		}
		tip = shas[won]
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
