package setup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repo makes a git repository with the given files, and returns its root.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Git reports the real path (a temp dir may sit behind a symlink); so does Init.
	top, err := Top(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return top
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
	return err == nil
}

func initRepo(t *testing.T, dir string) []string {
	t.Helper()
	changed, err := Init(context.Background(), dir)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	return changed
}

// init writes the marker into both files, creating the ones that are missing, and says the repo
// uses t4 so an agent there knows to ask its user about joining.
func TestInitWritesTheMarkerIntoBothFiles(t *testing.T) {
	dir := repo(t, map[string]string{"AGENTS.md": "# Notes\nkeep this\n"})
	if changed := initRepo(t, dir); len(changed) != 2 {
		t.Fatalf("changed %q, want AGENTS.md and CLAUDE.md", changed)
	}
	agents, claude := read(t, dir, "AGENTS.md"), read(t, dir, "CLAUDE.md")
	if !strings.HasPrefix(agents, "# Notes\nkeep this\n\n"+markStart) || strings.Count(agents, markStart) != 1 {
		t.Errorf("AGENTS.md:\n%s", agents)
	}
	if claude != section {
		t.Errorf("a CLAUDE.md that didn't exist is just the section:\n%s", claude)
	}
	for _, want := range []string{"Ask your user", "t4 join", "install.sh", "install.ps1"} {
		if !strings.Contains(agents, want) {
			t.Errorf("the section doesn't say %q:\n%s", want, agents)
		}
	}
	if !Marked(dir) {
		t.Error("Marked doesn't see the marker init wrote")
	}
	if changed := initRepo(t, dir); len(changed) != 0 {
		t.Errorf("a second init changed %q", changed)
	}
}

// A marker in either file is enough for a repo to count as one that uses t4, and no marker is not.
func TestMarkedNeedsAMarker(t *testing.T) {
	if Marked(repo(t, map[string]string{"AGENTS.md": "# Notes\n", "CLAUDE.md": "t4 is mentioned here\n"})) {
		t.Error("a repo with no marker is marked")
	}
	if !Marked(repo(t, map[string]string{"CLAUDE.md": markStart + "\nold\n" + markEnd + "\n"})) {
		t.Error("a repo with a marker in CLAUDE.md isn't marked")
	}
}

// A repo set up by an older init gets the current section in place of its old one, and nothing
// around it changes.
func TestInitRewritesTheSectionInPlace(t *testing.T) {
	old := "before\n\n" + markStart + "\n- call `claims`, then `claim` what you take\n" + markEnd + "\nafter\n"
	dir := repo(t, map[string]string{"AGENTS.md": old, "CLAUDE.md": "mine\n"})
	initRepo(t, dir)
	if want := "before\n\n" + section + "after\n"; read(t, dir, "AGENTS.md") != want {
		t.Errorf("AGENTS.md:\n%s\nwant:\n%s", read(t, dir, "AGENTS.md"), want)
	}
	if want := "mine\n\n" + section; read(t, dir, "CLAUDE.md") != want {
		t.Errorf("CLAUDE.md:\n%s", read(t, dir, "CLAUDE.md"))
	}
	if changed := initRepo(t, dir); len(changed) != 0 {
		t.Errorf("a second init changed %q", changed)
	}
}

func TestInitKeepsCRLF(t *testing.T) {
	old := "before\r\n\r\n" + markStart + "\r\nold\r\n" + markEnd + "\r\nafter\r\n"
	dir := repo(t, map[string]string{"AGENTS.md": old})
	initRepo(t, dir)
	got := read(t, dir, "AGENTS.md")
	if strings.Count(got, "\n") != strings.Count(got, "\r\n") {
		t.Errorf("mixed line endings:\n%q", got)
	}
	if changed := initRepo(t, dir); len(changed) != 0 {
		t.Errorf("a second init changed %q", changed)
	}
}

// A section whose end marker was deleted by hand is left as it is.
func TestInitLeavesABrokenSection(t *testing.T) {
	old := markStart + "\nhand-edited, no end marker\n"
	dir := repo(t, map[string]string{"AGENTS.md": old})
	initRepo(t, dir)
	if got := read(t, dir, "AGENTS.md"); got != old {
		t.Errorf("changed:\n%s", got)
	}
}

const oldMCP = `{
    "mcpServers": {
        "other": {"command": "other", "args": ["serve"]},
        "t4": {"command": "t4", "args": ["mcp"]}
    },
    "extra": [1, 2]
}
`

const oldSettings = `{
  "permissions": {"allow": ["Bash(ls)"]},
  "hooks": {
    "PostToolUse": [
      {"matcher": "*", "hooks": [{"type": "command", "command": "t4 hook"}]},
      {"matcher": "Edit", "hooks": [{"type": "command", "command": "other-hook"}]}
    ],
    "UserPromptSubmit": [
      {"hooks": [{"type": "command", "command": "t4 hook"}]}
    ]
  }
}
`

// init no longer puts the server or the hook into files that are committed. Where an older init
// did, it takes out those entries and nothing else.
func TestInitCleansAnOldInstall(t *testing.T) {
	dir := repo(t, map[string]string{".mcp.json": oldMCP, ".claude/settings.json": oldSettings})
	changed := initRepo(t, dir)
	if got := strings.Join(changed, "\n"); !strings.Contains(got, ".mcp.json") || !strings.Contains(got, ".claude/settings.json") {
		t.Errorf("init didn't say it cleaned both files:\n%s", got)
	}

	wantMCP := `{
    "mcpServers": {
        "other": {
            "command": "other",
            "args": [
                "serve"
            ]
        }
    },
    "extra": [
        1,
        2
    ]
}
`
	if got := read(t, dir, ".mcp.json"); got != wantMCP {
		t.Errorf(".mcp.json:\n%s\nwant:\n%s", got, wantMCP)
	}
	settings := read(t, dir, ".claude/settings.json")
	if strings.Contains(settings, "t4 hook") || !strings.Contains(settings, "other-hook") || !strings.Contains(settings, `"Bash(ls)"`) {
		t.Errorf(".claude/settings.json:\n%s", settings)
	}
	if strings.Contains(settings, "UserPromptSubmit") {
		t.Errorf("an event left with no hooks should be gone:\n%s", settings)
	}
	if changed := initRepo(t, dir); len(changed) != 0 {
		t.Errorf("a second init changed %q", changed)
	}
}

// A file that held nothing but what the older init wrote is deleted, with its directory.
func TestInitRemovesFilesItOnlyHeld(t *testing.T) {
	dir := repo(t, map[string]string{
		".mcp.json":             `{"mcpServers": {"t4": {"command": "t4", "args": ["mcp"]}}}`,
		".claude/settings.json": `{"hooks": {"PostToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "t4 hook"}]}]}}`,
	})
	initRepo(t, dir)
	if exists(dir, ".mcp.json") || exists(dir, ".claude/settings.json") || exists(dir, ".claude") {
		t.Error("the emptied files are still there")
	}
}

// A t4 entry that isn't the one init wrote (another program with that name, or the user's own
// settings) is not init's to remove.
func TestInitLeavesEntriesThatAreNotItsOwn(t *testing.T) {
	mcp := `{"mcpServers": {"t4": {"command": "node", "args": ["my-t4.js"]}}}`
	settings := `{"hooks": {"PostToolUse": [{"hooks": [{"type": "command", "command": "other t4 hook"}]}]}}`
	dir := repo(t, map[string]string{".mcp.json": mcp, ".claude/settings.json": settings})
	initRepo(t, dir)
	if read(t, dir, ".mcp.json") != mcp || read(t, dir, ".claude/settings.json") != settings {
		t.Error("init changed an entry it didn't write")
	}
}

// What join writes is recognised, wherever the binary is, and nothing else is.
func TestIsHookCommand(t *testing.T) {
	for command, want := range map[string]bool{
		"t4 hook":                        true,
		`"C:/Users/a b/bin/t4.exe" hook`: true,
		`"/home/a/.local/bin/t4" hook`:   true,
		`"/home/a/\$tools/t4" hook`:      true,
		`"/home/a/\"q\"/t4" hook`:        true,
		"/usr/local/bin/t4 hook":         true,
		"t4 hook --extra":                false,
		"t4 who":                         false,
		"other t4 hook":                  false,
		`"/home/a/bin/not-t4" hook`:      false,
		`"unterminated t4 hook`:          false,
		"":                               false,
	} {
		if got := IsHookCommand(command); got != want {
			t.Errorf("IsHookCommand(%q) = %v, want %v", command, got, want)
		}
	}
}

// A shell runs the hook command, so a path with a space, a dollar sign, a backtick or a quote in
// it must reach the binary as written: HookCommand is run through a real shell here, and then
// recognised again by IsHookCommand (Codex review, 2026-10-29).
func TestHookCommandReachesTheBinaryThroughAShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to run the command in")
	}
	names := []string{"a b", "$HOME", "x`echo wrong`", "$(echo wrong)", `back\slash`}
	if runtime.GOOS != "windows" {
		names = append(names, `q"uote`) // not a legal file name there
	}
	for _, name := range names {
		bin := filepath.Join(t.TempDir(), name, "t4")
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bin, []byte("#!/bin/sh\necho ran\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		command := HookCommand(bin)
		out, err := exec.Command(sh, "-c", command).CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "ran" {
			t.Errorf("%s: the shell ran something else: %q, %v\n%s", name, command, err, out)
		}
		if !IsHookCommand(command) {
			t.Errorf("%s: %q isn't recognised as t4's hook", name, command)
		}
	}
}

// On Windows the hook's path has forward slashes, which bash reads and a backslash it would not.
func TestHookCommandUsesForwardSlashesOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows paths have backslashes to convert")
	}
	if got, want := HookCommand(`C:\Users\a b\bin\t4.exe`), `"C:/Users/a b/bin/t4.exe" hook`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
