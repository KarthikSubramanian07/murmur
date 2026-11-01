package gitx_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/a-Fig/murmur/internal/gitx"
)

// A directory in one repository that is a link into another is in the other, as git sees it: git
// resolves the link before it looks for .git. Discover leaves that to git rather than name the
// repository the link sits in, whose daemon would then be told of another clone's tool calls.
func TestDiscoverLeavesLinksToGit(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, dir := range []string{a, b} {
		if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
	}
	target := filepath.Join(b, "sub")
	if err := os.MkdirAll(filepath.Join(target, "deeper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if gitDir, _, ok := gitx.Discover(filepath.Join(target, "deeper")); !ok || gitDir != filepath.Join(b, ".git") {
		t.Fatalf("Discover in a plain subdirectory = %q, %v; want %q", gitDir, ok, filepath.Join(b, ".git"))
	}
	link := filepath.Join(a, "link")
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS != "windows" {
			t.Fatal(err)
		}
		// Making a symbolic link takes a privilege on Windows; a junction doesn't.
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Skipf("can't make a link: %v\n%s", err, out)
		}
	}
	want, err := os.Stat(filepath.Join(b, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{link, filepath.Join(link, "deeper")} {
		gitDir, _, ok := gitx.Discover(dir)
		if !ok {
			continue // git is asked
		}
		if got, err := os.Stat(gitDir); err != nil || !os.SameFile(got, want) {
			t.Errorf("Discover(%s) = %q, the repository the link is in; git finds %q", dir, gitDir, filepath.Join(b, ".git"))
		}
	}
}
