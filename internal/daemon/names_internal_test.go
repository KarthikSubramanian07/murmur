package daemon

import (
	"context"
	"io"
	"log"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/a-Fig/t4/internal/brand"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// namedDaemon is a daemon in a clone of a bare remote that has just taken its first session name,
// so its registry is on the remote and recorded as read.
func namedDaemon(t *testing.T) (d *Daemon, remote string) {
	t.Helper()
	root := t.TempDir()
	remote = filepath.Join(root, "remote.git")
	runGit(t, root, "init", "-q", "--bare", remote)
	clone := filepath.Join(root, "clone")
	runGit(t, root, "clone", "-q", "--no-local", remote, clone)
	opt := Options{GitDir: filepath.Join(clone, ".git"), Log: log.New(io.Discard, "", 0)}
	opt.defaults()
	d, err := open(context.Background(), opt, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.attemptName(context.Background(), &wanted{id: "aaaaaa", key: nameKey("", "", "aaaaaa")}); err != nil {
		t.Fatal(err)
	}
	if d.st.NamesGood == "" || d.st.NamesGood != d.st.NamesTip {
		t.Fatalf("after the first name the registry's tip is %q and the last read in full %q", d.st.NamesTip, d.st.NamesGood)
	}
	return d, remote
}

// A sync lists the remote before it takes advMu, and this daemon's own first name can be pushed
// and recorded in between: the registry is on the remote and read, and the listing says it isn't
// there. That is a registry that hasn't been made yet, not one that was deleted, and must not set
// the registry to be repaired, which would replace its history with a snapshot (#80).
func TestListingFromBeforeTheFirstNameIsNotALostRegistry(t *testing.T) {
	d, _ := namedDaemon(t)
	ctx := context.Background()
	tip := d.st.NamesTip

	d.advMu.Lock()
	d.processNames(ctx, "") // what pollOnce does with a listing that has no registry
	d.advMu.Unlock()

	if d.st.NamesRepair {
		t.Error("a listing from before the registry was made set it to be repaired")
	}
	if d.st.NamesTip != tip || d.st.NamesGood != tip {
		t.Errorf("the registry's tip became %q, last read in full %q; want both %q", d.st.NamesTip, d.st.NamesGood, tip)
	}
	if w, repair := d.nextName(); w != nil || repair {
		t.Errorf("the assigner has work to do: session %v, repair %v", w, repair)
	}
	if got, err := d.repo.RemoteRef(ctx, brand.NamesRef); err != nil || got != tip {
		t.Errorf("the registry on the remote is %q (%v); it should still be %q", got, err, tip)
	}
}

// A registry that is gone from the remote after having been read is lost, however the daemon
// found out: the check above must not hide a real deletion.
func TestRegistryDeletedFromTheRemoteIsStillALostRegistry(t *testing.T) {
	d, remote := namedDaemon(t)
	runGit(t, t.TempDir(), "--git-dir", remote, "update-ref", "-d", brand.NamesRef)

	d.advMu.Lock()
	d.processNames(context.Background(), "")
	d.advMu.Unlock()

	if !d.st.NamesRepair {
		t.Error("a registry deleted from the remote was not set to be repaired")
	}
	if w, repair := d.nextName(); w != nil || !repair {
		t.Errorf("the assigner has session %v and repair %v; want a repair and no session", w, repair)
	}
}
