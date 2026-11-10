package bus_test

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/gitx"
	"github.com/a-Fig/murmur/internal/testgit"
)

// pushPair is a bare remote and a bare clone that pushes to it.
func pushPair(t *testing.T) (remote string, r *bus.Repo) {
	t.Helper()
	root := t.TempDir()
	remote, local := filepath.Join(root, "remote.git"), filepath.Join(root, "local.git")
	for _, d := range []string{remote, local} {
		if out, err := exec.Command("git", "init", "-q", "--bare", d).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
	}
	return remote, &bus.Repo{Git: gitx.Git{Dir: local}, Remote: remote, Name: "test", Email: "test@example.invalid"}
}

// GitHub now and then refuses a push that meets another push to the same repository, with
// "[remote rejected] (failed)" or "(fatal error in commit_refs)". The same push sent again lands,
// so Push sends it again, in each mode, and the ref ends where the push put it.
func TestPushSendsBusyRefusalsAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, reason := range []string{"failed", "fatal error in commit_refs"} {
		for _, mode := range []bus.PushMode{bus.FastForward, bus.Force, bus.Lease} {
			remote, r := pushPair(t)
			ref := brand.RefRoot + "busy"
			first, err := r.NewCommit(ctx, "", "first")
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Push(ctx, first, ref, bus.FastForward, ""); err != nil {
				t.Fatal(err)
			}
			next, err := r.NewCommit(ctx, first, "next")
			if err != nil {
				t.Fatal(err)
			}
			refused := testgit.RefusePushes(t, remote, brand.RefRoot, reason, 1, false)
			if err := r.Push(ctx, next, ref, mode, first); err != nil {
				t.Fatalf("(%s), mode %d: %v", reason, mode, err)
			}
			if n := refused(); n != 1 {
				t.Fatalf("(%s), mode %d: the remote refused %d pushes, want the 1 it was set to", reason, mode, n)
			}
			if now, err := r.RemoteRef(ctx, ref); err != nil || now != next {
				t.Fatalf("(%s), mode %d: the remote holds %s (%v), want %s", reason, mode, now, err, next)
			}
		}
	}
}

// A remote that stays busy is tried a few times, and then Push says the server refused, with its
// reason; a refusal for any other reason is the server's answer, and isn't sent again.
func TestPushGivesUpOnRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, c := range []struct {
		reason string
		tries  int
	}{{"failed", 3}, {"protected by a rule", 1}, {"missing necessary objects", 1}} {
		remote, r := pushPair(t)
		sha, err := r.NewCommit(ctx, "", "a commit")
		if err != nil {
			t.Fatal(err)
		}
		refused := testgit.RefusePushes(t, remote, brand.RefRoot, c.reason, 100, false)
		err = r.Push(ctx, sha, brand.RefRoot+"busy", bus.Force, "")
		if !errors.Is(err, bus.ErrRefused) || errors.Is(err, bus.ErrRejected) {
			t.Errorf("(%s): %v, want ErrRefused", c.reason, err)
		}
		if n := refused(); n != c.tries {
			t.Errorf("(%s): sent %d times, want %d", c.reason, n, c.tries)
		}
	}
}

// A push the server took but called failed has landed: it isn't sent again, and nothing is written
// twice.
func TestPushRefusedButLandedIsNotSentAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote, r := pushPair(t)
	sha, err := r.NewCommit(ctx, "", "a commit")
	if err != nil {
		t.Fatal(err)
	}
	refused := testgit.RefusePushes(t, remote, brand.RefRoot, "failed", 100, true)
	ref := brand.RefRoot + "busy"
	if err := r.Push(ctx, sha, ref, bus.Lease, ""); err != nil {
		t.Fatal(err)
	}
	if n := refused(); n != 1 {
		t.Fatalf("sent %d times, want once", n)
	}
	if now, err := r.RemoteRef(ctx, ref); err != nil || now != sha {
		t.Fatalf("the remote holds %s (%v), want %s", now, err, sha)
	}
}
