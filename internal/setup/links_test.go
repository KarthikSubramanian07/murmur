package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// linkInRepo makes name, in the working tree at dir, a symbolic link to target.
func linkInRepo(t *testing.T, dir, name, target string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
}

// init --no-push refuses a marker file that a link takes out of the repo, as Push does: a commit
// can point CLAUDE.md at a shell's startup file, and init would append to it.
func TestInitLeavesALinkOutOfTheRepoAlone(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := repo(t, map[string]string{"AGENTS.md": "# Notes\n"})
	linkInRepo(t, dir, "CLAUDE.md", outside)
	_, err := Init(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "outside the repo") {
		t.Errorf("init with CLAUDE.md linked out of the repo: %v", err)
	}
	if got, _ := os.ReadFile(outside); string(got) != "mine\n" {
		t.Errorf("init wrote through the link:\n%s", got)
	}
}

// A link to a file that isn't there would be created wherever it points, and a link into .git
// would put markdown in git's own files: init refuses both.
func TestInitLeavesADanglingLinkAndALinkIntoGitAlone(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.md")
	dir := repo(t, map[string]string{"AGENTS.md": "# Notes\n"})
	linkInRepo(t, dir, "CLAUDE.md", missing)
	if _, err := Init(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "isn't there") {
		t.Errorf("init with a dangling CLAUDE.md: %v", err)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("init created the file a dangling link points to")
	}

	dir = repo(t, map[string]string{"AGENTS.md": "# Notes\n"})
	config := read(t, dir, ".git/config")
	linkInRepo(t, dir, "CLAUDE.md", filepath.Join(".git", "config"))
	if _, err := Init(context.Background(), dir); err == nil || !strings.Contains(err.Error(), ".git") {
		t.Errorf("init with CLAUDE.md linked into .git: %v", err)
	}
	if got := read(t, dir, ".git/config"); got != config {
		t.Errorf("init wrote into .git/config:\n%s", got)
	}
}

// A config file init only cleans is passed over when a link takes it out of the repo, as Push
// passes it over, rather than rewritten or deleted there.
func TestInitLeavesAConfigLinkedOutOfTheRepoAlone(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(outside, []byte(oldMCP), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := repo(t, nil)
	linkInRepo(t, dir, ".mcp.json", outside)
	initRepo(t, dir)
	if got, _ := os.ReadFile(outside); string(got) != oldMCP {
		t.Errorf("init rewrote a config file out of the repo:\n%s", got)
	}
}

// A link that stays in the repo, as CLAUDE.md to AGENTS.md, is followed: the section goes into
// AGENTS.md once.
func TestInitFollowsALinkWithinTheRepo(t *testing.T) {
	dir := repo(t, map[string]string{"AGENTS.md": "# Notes\n"})
	linkInRepo(t, dir, "CLAUDE.md", "AGENTS.md")
	initRepo(t, dir)
	if got := read(t, dir, "AGENTS.md"); strings.Count(got, markStart) != 1 {
		t.Errorf("AGENTS.md:\n%s", got)
	}
	if fi, err := os.Lstat(filepath.Join(dir, "CLAUDE.md")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("CLAUDE.md isn't a link any more (%v)", err)
	}
}
