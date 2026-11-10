package setup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// within fails the test if f hasn't returned in a few seconds: a read that follows a link to a
// device never ends by itself.
func within(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s is still reading after 10 s", what)
	}
}

func linkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
}

// CLAUDE.md is often a link to AGENTS.md. The marker behind the link counts, and init puts the
// section in AGENTS.md once and leaves the link a link.
func TestALinkToAGENTSmdIsReadThrough(t *testing.T) {
	dir := repo(t, map[string]string{"AGENTS.md": "# Notes\n"})
	linkOrSkip(t, "AGENTS.md", filepath.Join(dir, "CLAUDE.md"))
	initRepo(t, dir)
	if got := read(t, dir, "AGENTS.md"); strings.Count(got, markStart) != 1 {
		t.Errorf("AGENTS.md:\n%s", got)
	}
	if fi, err := os.Lstat(filepath.Join(dir, "CLAUDE.md")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("CLAUDE.md isn't a link any more (%v)", err)
	}

	// A marker only behind a link counts too.
	dir = repo(t, map[string]string{"docs/agents.md": section})
	linkOrSkip(t, filepath.Join("docs", "agents.md"), filepath.Join(dir, "CLAUDE.md"))
	if !markedTree(dir) {
		t.Error("the marker behind CLAUDE.md's link isn't seen")
	}
}

// A marker file a commit made a link to a device is passed over, quickly: every session's MCP
// server checks the marker every 2 seconds, and /dev/zero never ends. The other file's marker
// still counts, and init stops rather than read it.
func TestAMarkerFileThatIsALinkToADeviceIsNotRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/zero on Windows")
	}
	dir := repo(t, map[string]string{"CLAUDE.md": section})
	linkOrSkip(t, "/dev/zero", filepath.Join(dir, "AGENTS.md"))
	within(t, "the marker check", func() {
		if !markedTree(dir) {
			t.Error("CLAUDE.md's marker isn't seen past AGENTS.md's link to /dev/zero")
		}
	})
	if err := os.Remove(filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	within(t, "the marker check", func() {
		if markedTree(dir) {
			t.Error("a link to /dev/zero counts as marked")
		}
	})
	// Init refuses it, as not a regular file or, where links out of the repo are refused first,
	// as one of those; either way before reading it.
	within(t, "init", func() {
		if _, err := Init(context.Background(), dir); err == nil {
			t.Error("init took a link to /dev/zero")
		}
	})
}

// A marker file over maxRepoFile isn't one an agent reads: it doesn't count, even with the marker
// at its end, and init leaves it as it is rather than rewrite part of it.
func TestAMarkerFileOverTheLimitIsNotRead(t *testing.T) {
	big := append(bytes.Repeat([]byte("x"), maxRepoFile), "\n"+section...)
	dir := repo(t, map[string]string{"AGENTS.md": string(big)})
	if markedTree(dir) {
		t.Error("a marker past the limit counts")
	}
	if _, err := Init(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "MiB") {
		t.Errorf("init on a file over the limit: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "AGENTS.md")); err != nil || !bytes.Equal(got, big) {
		t.Errorf("init changed a file over the limit (%v)", err)
	}
}

// A file just under the limit is read, but the section would take it over, after which the marker
// check wouldn't read it: init stops rather than report a repo marked that isn't (Sol's review).
func TestInitDoesNotTakeAMarkerFileOverTheLimit(t *testing.T) {
	full := strings.Repeat(strings.Repeat("x", 63)+"\n", maxRepoFile/64)
	dir := repo(t, map[string]string{"AGENTS.md": full, "CLAUDE.md": full})
	if _, err := Init(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "MiB") {
		t.Errorf("init on files at the limit: %v", err)
	}
	for _, name := range markerFiles {
		if got := read(t, dir, name); got != full {
			t.Errorf("init changed %s, %d bytes now", name, len(got))
		}
	}
	if markedTree(dir) {
		t.Error("marked, with no marker")
	}
}
