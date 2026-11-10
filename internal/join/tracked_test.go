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
	changed, notes, err := installHooks(context.Background(), p, exe)
	if err != nil || len(notes) != 1 || !strings.Contains(notes[0], "symbolic link the repo tracks") || !strings.Contains(notes[0], "git rm") {
		t.Errorf("join on a tracked link: notes %q, err %v", notes, err)
	}
	if exists(outside) {
		t.Errorf("join wrote through the tracked link:\n%s", read(t, outside))
	}
	if len(changed) != 1 || filepath.Base(changed[0]) != filepath.Base(other) {
		t.Errorf("changed %q, want only the other worktree", changed)
	}

	write(t, outside, "{\"keep\": true}\n")
	_, notes, err = removeHooks(context.Background(), p)
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

	if _, notes, err := installHooks(context.Background(), p, filepath.Join(t.TempDir(), "mur")); err != nil || len(notes) != 1 || !strings.Contains(notes[0], "symbolic link the repo tracks") {
		t.Errorf("join on a tracked .claude link: notes %q, err %v", notes, err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("join wrote into the folder the tracked link points to: %v", entries)
	}
}

// The same links committed under names that differ only in case lead to the same file on a disk
// that ignores case, as macOS's and Windows' do by default, so they are refused too, there and
// everywhere else.
func TestLinksTheRepoTracksAreFoundWhateverTheirCase(t *testing.T) {
	for _, name := range []string{".Claude", ".claude/Settings.Local.json", ".CLAUDE/SETTINGS.LOCAL.JSON"} {
		t.Run(name, func(t *testing.T) {
			p, _ := clone(t)
			outside := t.TempDir()
			target := outside
			if strings.Contains(name, "/") {
				target = filepath.Join(outside, "settings.json")
			}
			commitLink(t, p.Top, name, target)
			_, notes, err := installHooks(context.Background(), p, filepath.Join(t.TempDir(), "mur"))
			if err != nil || len(notes) != 1 || !strings.Contains(notes[0], "symbolic link the repo tracks") {
				t.Errorf("join on a tracked %s link: notes %q, err %v", name, notes, err)
			}
			if entries, _ := os.ReadDir(outside); len(entries) != 0 {
				t.Errorf("join wrote where the tracked %s link points: %v", name, entries)
			}
		})
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
	if _, notes, err := installHooks(context.Background(), p, exe); err != nil || len(notes) != 1 || !strings.Contains(notes[0], "git rm --cached") || !strings.Contains(notes[0], "copy theirs aside") {
		t.Errorf("join on a tracked hook file: notes %q, err %v", notes, err)
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

// The index can track names that differ only in case. A plain .Claude sorts before a .claude link,
// and on a disk that ignores case the link may be what join and leave open: a link among the
// entries counts, whichever comes first (Sol's review). Index entries alone show it, with no link
// on disk, so this runs where links can't be made.
func TestATrackedLinkIsFoundBehindAPlainEntryInAnotherCase(t *testing.T) {
	p, _ := clone(t)
	empty := filepath.Join(t.TempDir(), "empty")
	write(t, empty, "")
	git(t, p.Top, "hash-object", "-w", empty)
	const blob = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391" // the empty blob, just written
	for _, c := range []struct{ plain, link string }{
		{".Claude", ".claude"},
		{".claude/Settings.local.json", ".claude/settings.local.json"},
	} {
		git(t, p.Top, "rm", "-q", "--cached", "-r", "--ignore-unmatch", "--", ".claude", ".Claude")
		git(t, p.Top, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+c.plain)
		git(t, p.Top, "update-index", "--add", "--cacheinfo", "120000,"+blob+","+c.link)
		path, link, err := trackedHookPath(context.Background(), p.Top)
		if err != nil || !link || path != c.link {
			t.Errorf("%s beside a %s link: got %q, link %v, err %v", c.plain, c.link, path, link, err)
		}
	}
}

// With GIT_LITERAL_PATHSPECS set, git reads a ":(icase)" pathspec as a file's name; the check must
// still find a tracked link, or fail, never say nothing is tracked.
func TestTrackedHookLinkIsFoundWithLiteralPathspecs(t *testing.T) {
	p, _ := clone(t)
	outside := filepath.Join(t.TempDir(), "settings.json")
	commitLink(t, p.Top, ".Claude/settings.local.json", outside)
	t.Setenv("GIT_LITERAL_PATHSPECS", "1")
	path, _, err := trackedHookPath(context.Background(), p.Top)
	if err == nil && path == "" {
		t.Fatal("a tracked hook link went unseen with GIT_LITERAL_PATHSPECS set")
	}
	if _, _, err := installHooks(context.Background(), p, filepath.Join(t.TempDir(), "mur")); err == nil && exists(outside) {
		t.Errorf("join wrote through the tracked link:\n%s", read(t, outside))
	}
}
