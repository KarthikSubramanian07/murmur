package join

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func linkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
}

func mustBeLink(t *testing.T, path string) {
	t.Helper()
	if fi, err := os.Lstat(path); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("%s isn't a link any more (%v)", path, err)
	}
}

// A link to a file that doesn't exist yet (a dotfiles repository's, not created) is written
// through as well: the file appears where the link points and the link stays (Codex review,
// 2026-10-29). EvalSymlinks fails on such a link, and a write that gave up there replaced it.
func TestRegisterCodexWritesThroughADanglingSymlink(t *testing.T) {
	sb := newSandbox(t, false)
	link := filepath.Join(sb.CodexHome, "config.toml")
	linkOrSkip(t, filepath.Join("..", "dotfiles", "codex.toml"), link)
	if _, _, err := sb.registerCodex(); err != nil {
		t.Fatal(err)
	}
	mustBeLink(t, link)
	if got := read(t, filepath.Join(sb.root, "dotfiles", "codex.toml")); !strings.Contains(got, "[mcp_servers.t4]") {
		t.Errorf("the file the link points to:\n%s", got)
	}
}

// A hook file that is a link is written through when the hook goes in, and again when it comes out,
// even when what is left is nothing: the link is the user's and stays, with the file it points to
// emptied rather than deleted (Codex review, 2026-10-29).
func TestHookFileThatIsALinkIsKeptThroughJoinAndLeave(t *testing.T) {
	p, _ := clone(t)
	target := filepath.Join(t.TempDir(), "settings.json")
	write(t, target, "{}\n")
	link := filepath.Join(p.Top, ".claude", "settings.local.json")
	linkOrSkip(t, target, link)

	exe := filepath.Join(t.TempDir(), "bin", "t4")
	if _, err := installHooks(context.Background(), p, exe); err != nil {
		t.Fatal(err)
	}
	mustBeLink(t, link)
	if events := hookEvents(t, target); !reflect.DeepEqual(events, wantHookEvents()) {
		t.Errorf("the file the link points to has the hook on %v", events)
	}

	if _, err := removeHooks(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	mustBeLink(t, link)
	if got := read(t, target); strings.Contains(got, "hook") {
		t.Errorf("the hook is still in the file the link points to:\n%s", got)
	}
}
