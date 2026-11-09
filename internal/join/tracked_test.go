package join

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/setup"
)

// commitLink commits, in the working tree at top, a symbolic link at name to target.
func commitLink(t *testing.T, top, name, target string) {
	t.Helper()
	linkOrSkip(t, target, filepath.Join(top, filepath.FromSlash(name)))
	git(t, top, "add", "--", name)
	git(t, top, "commit", "-q", "-m", "link")
}

// A hook file the repo tracks as a link points wherever the commit chose, the user's global Claude
// Code settings included. Join doesn't write through it, says so, and still puts the hook in the
// clone's other worktrees; leave doesn't write through it either.
func TestHookFileTheRepoTracksAsALinkIsLeftAlone(t *testing.T) {
	p, other := clone(t)
	outside := filepath.Join(t.TempDir(), "settings.json")
	commitLink(t, p.Top, ".claude/settings.local.json", outside)

	exe := filepath.Join(t.TempDir(), "bin", "mur")
	changed, err := installHooks(context.Background(), p, exe)
	if err == nil || !strings.Contains(err.Error(), "symbolic link the repo tracks") || !strings.Contains(err.Error(), "git rm") {
		t.Errorf("join on a tracked link: %v", err)
	}
	if exists(outside) {
		t.Errorf("join wrote through the tracked link:\n%s", read(t, outside))
	}
	if len(changed) != 1 || filepath.Base(changed[0]) != filepath.Base(other) {
		t.Errorf("changed %q, want only the other worktree", changed)
	}

	write(t, outside, "{\"keep\": true}\n")
	_, notes, err := removeHooks(context.Background(), p)
	if err != nil || len(notes) != 1 || !strings.Contains(notes[0], "didn't write through it") {
		t.Errorf("leave on a tracked link: notes %q, err %v", notes, err)
	}
	if got := read(t, outside); got != "{\"keep\": true}\n" {
		t.Errorf("leave wrote through the tracked link:\n%s", got)
	}
}

// .claude itself committed as a link to a folder outside the repo is refused the same way.
func TestClaudeFolderTheRepoTracksAsALinkIsLeftAlone(t *testing.T) {
	p, _ := clone(t)
	outside := t.TempDir()
	commitLink(t, p.Top, ".claude", outside)

	if _, err := installHooks(context.Background(), p, filepath.Join(t.TempDir(), "mur")); err == nil || !strings.Contains(err.Error(), "symbolic link the repo tracks") {
		t.Errorf("join on a tracked .claude link: %v", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("join wrote into the folder the tracked link points to: %v", entries)
	}
}

// A plain hook file the repo tracks isn't the user's: join won't put this machine's path to mur in
// it, where the next commit could take it. Leave still takes out a hook an older join put there,
// and keeps the file.
func TestHookFileTheRepoTracksIsNotJoinedInto(t *testing.T) {
	p, _ := clone(t)
	path := filepath.Join(p.Top, ".claude", "settings.local.json")
	write(t, path, "{\n  \"permissions\": {\"allow\": [\"Bash(ls)\"]}\n}\n")
	git(t, p.Top, "add", "--", ".claude/settings.local.json")
	git(t, p.Top, "commit", "-q", "-m", "settings")

	exe := filepath.Join(t.TempDir(), "bin", "mur")
	if _, err := installHooks(context.Background(), p, exe); err == nil || !strings.Contains(err.Error(), "git rm --cached") {
		t.Errorf("join on a tracked hook file: %v", err)
	}
	if got := read(t, path); strings.Contains(got, "hook") {
		t.Errorf("join edited a tracked hook file:\n%s", got)
	}

	if _, err := installHook(p.Top, setup.HookCommand(exe)); err != nil { // as a join before this check did
		t.Fatal(err)
	}
	removed, notes, err := removeHooks(context.Background(), p)
	if err != nil || len(notes) != 0 || len(removed) != 2 { // this worktree, and the other one join could put the hook in
		t.Fatalf("leave: removed %q, notes %q, err %v", removed, notes, err)
	}
	if got := read(t, path); strings.Contains(got, "hook") || !strings.Contains(got, "Bash(ls)") {
		t.Errorf("after leave, the tracked file:\n%s", got)
	}
}
