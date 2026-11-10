package daemon

import (
	"context"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/names"
)

// freshNames returns n valid session names, distinct, from a stream seeded with seed.
func freshNames(t *testing.T, seed byte, n int) []string {
	t.Helper()
	var key [32]byte
	key[0] = seed
	src, seen := rand.NewChaCha8(key), map[string]bool{}
	var out []string
	for len(out) < n {
		name, err := names.Generate(src)
		if err != nil {
			t.Fatal(err)
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// Murmur writes a snapshot of the used names only on a new root, as a repair's first commit. One on
// any other commit isn't Murmur's: anyone with push access could append one listing every name, and
// every daemon that read it would hold them all used, for good, and write them back in its repairs.
// A snapshot on a root is still read, as a repair needs.
func TestASnapshotCountsOnlyOnARoot(t *testing.T) {
	d, _ := namedDaemon(t)
	ctx := context.Background()
	fake := freshNames(t, 91, 2)
	snapshot := func(parent string, used map[string]string) string {
		t.Helper()
		rec := bus.NameRecord{V: bus.Version, At: time.Now().UTC(), Used: used}
		msg, err := bus.Encode(rec, rec.Subject())
		if err != nil {
			t.Fatal(err)
		}
		sha, err := d.repo.NewCommit(ctx, parent, msg)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.repo.UpdateLocal(ctx, brand.NamesRef, sha); err != nil {
			t.Fatal(err)
		}
		return sha
	}

	appended := snapshot(d.st.NamesTip, map[string]string{fake[0]: "", fake[1]: "x-0001/s1"})
	d.advMu.Lock()
	d.processNames(ctx, appended)
	d.advMu.Unlock()
	for _, name := range fake {
		if _, ok := d.st.Used[name]; ok {
			t.Errorf("%s, in a snapshot on a commit that isn't a root, was taken for used", name)
		}
	}
	if d.st.NamesTip != appended {
		t.Errorf("the registry's tip is %s, want the commit read past, %s", d.st.NamesTip, appended)
	}

	// A root merged in comes on a fast-forward, with no rewrite to notice: it isn't the history's own.
	merged := snapshot("", map[string]string{fake[1]: ""})
	tree, err := d.repo.EmptyTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	merge := strings.TrimSpace(runGit(t, t.TempDir(), "--git-dir", d.opt.GitDir, "-c", "user.name=t", "-c", "user.email=t@example.com",
		"commit-tree", tree, "-p", appended, "-p", merged, "-m", "merge"))
	if err := d.repo.UpdateLocal(ctx, brand.NamesRef, merge); err != nil {
		t.Fatal(err)
	}
	d.advMu.Lock()
	d.processNames(ctx, merge)
	d.advMu.Unlock()
	if _, ok := d.st.Used[fake[1]]; ok {
		t.Errorf("%s, in a snapshot on a root merged in, was taken for used", fake[1])
	}
	if d.st.NamesRepair || d.st.NamesTip != merge {
		t.Errorf("the merge was taken for a rewrite (repair %v), or not read (tip %s)", d.st.NamesRepair, d.st.NamesTip)
	}

	root := snapshot("", map[string]string{fake[0]: ""})
	d.advMu.Lock()
	d.processNames(ctx, root)
	d.advMu.Unlock()
	if _, ok := d.st.Used[fake[0]]; !ok {
		t.Errorf("%s, in a root's snapshot, wasn't taken for used", fake[0])
	}
}
