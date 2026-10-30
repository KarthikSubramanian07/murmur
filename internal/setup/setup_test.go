package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func agentsFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if content != "" {
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func readAgents(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAgentsMDAddsOnce(t *testing.T) {
	dir := agentsFile(t, "# Notes\nkeep this\n")
	if msg, err := agentsMD(dir); err != nil || msg == "" {
		t.Fatalf("first run: %q, %v", msg, err)
	}
	if msg, err := agentsMD(dir); err != nil || msg != "" {
		t.Fatalf("second run changed the file: %q, %v", msg, err)
	}
	got := readAgents(t, dir)
	if !strings.HasPrefix(got, "# Notes\nkeep this\n\n") || strings.Count(got, markStart) != 1 {
		t.Errorf("unexpected AGENTS.md:\n%s", got)
	}
}

// A repo set up by an older version gets the current section in place of its old one, and
// nothing around it changes.
func TestAgentsMDReplacesOldSection(t *testing.T) {
	old := "before\n\n" + markStart + "\n- call `claims`, then `claim` what you take\n" + markEnd + "\nafter\n"
	dir := agentsFile(t, old)
	if msg, err := agentsMD(dir); err != nil || !strings.Contains(msg, "updated") {
		t.Fatalf("got %q, %v", msg, err)
	}
	got := readAgents(t, dir)
	if want := "before\n\n" + agentsSection + "after\n"; got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if msg, _ := agentsMD(dir); msg != "" {
		t.Errorf("a second run changed it again: %q", msg)
	}
}

func TestAgentsMDKeepsCRLF(t *testing.T) {
	old := "before\r\n\r\n" + markStart + "\r\nold\r\n" + markEnd + "\r\nafter\r\n"
	dir := agentsFile(t, old)
	if _, err := agentsMD(dir); err != nil {
		t.Fatal(err)
	}
	got := readAgents(t, dir)
	if strings.Count(got, "\n") != strings.Count(got, "\r\n") {
		t.Errorf("mixed line endings:\n%q", got)
	}
	if msg, _ := agentsMD(dir); msg != "" {
		t.Errorf("a second run changed it again: %q", msg)
	}
}

// A section whose end marker was deleted by hand is left as it is.
func TestAgentsMDLeavesBrokenSection(t *testing.T) {
	old := markStart + "\nhand-edited, no end marker\n"
	dir := agentsFile(t, old)
	if msg, err := agentsMD(dir); err != nil || msg != "" {
		t.Fatalf("got %q, %v", msg, err)
	}
	if got := readAgents(t, dir); got != old {
		t.Errorf("changed:\n%s", got)
	}
}

// hookCommands reads which events .claude/settings.json runs the t4 hook on, with each event's matcher.
func hookCommands(t *testing.T, top string) map[string]string {
	t.Helper()
	cfg, err := readJSON(filepath.Join(top, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	hooks, _ := cfg["hooks"].(map[string]any)
	for event, v := range hooks {
		groups, _ := v.([]any)
		if hasCommand(groups, hookCommand) {
			matcher := ""
			if g, _ := groups[len(groups)-1].(map[string]any); g != nil {
				matcher, _ = g["matcher"].(string)
			}
			out[event] = matcher
		}
	}
	return out
}

// A repo set up before tool calls were bracketed has the hook on PostToolUse and UserPromptSubmit
// only. Running init again adds PreToolUse (and PostToolUseFailure) without duplicating or
// disturbing what is there (#21).
func TestClaudeHooksAddsPreToolUseToAnExistingInstall(t *testing.T) {
	top := t.TempDir()
	old := `{
  "permissions": {"allow": ["Bash(go test:*)"]},
  "hooks": {
    "PostToolUse": [
      {"matcher": "Edit", "hooks": [{"type": "command", "command": "other-tool"}]},
      {"matcher": "*", "hooks": [{"type": "command", "command": "` + hookCommand + `"}]}
    ],
    "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "` + hookCommand + `"}]}]
  }
}`
	if err := os.MkdirAll(filepath.Join(top, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(top, ".claude", "settings.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	msg, err := claudeHooks(top)
	if err != nil || !strings.Contains(msg, "PreToolUse") {
		t.Fatalf("first run: %q, %v", msg, err)
	}
	got := hookCommands(t, top)
	for _, event := range []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "UserPromptSubmit"} {
		if _, ok := got[event]; !ok {
			t.Errorf("no hook on %s: %v", event, got)
		}
	}
	if got["PreToolUse"] != "*" {
		t.Errorf("PreToolUse matcher = %q, want *", got["PreToolUse"])
	}

	cfg, _ := readJSON(filepath.Join(top, ".claude", "settings.json"))
	if _, ok := cfg["permissions"]; !ok {
		t.Error("the other settings were lost")
	}
	post, _ := cfg["hooks"].(map[string]any)["PostToolUse"].([]any)
	if len(post) != 2 {
		t.Errorf("PostToolUse had its groups changed: %v", post)
	}

	if msg, err := claudeHooks(top); err != nil || msg != "" {
		t.Errorf("second run changed it: %q, %v", msg, err)
	}
}

// A repo with no settings gets the hook on every event it needs.
func TestClaudeHooksOnAFreshRepo(t *testing.T) {
	top := t.TempDir()
	if msg, err := claudeHooks(top); err != nil || msg == "" {
		t.Fatalf("got %q, %v", msg, err)
	}
	got := hookCommands(t, top)
	want := map[string]string{"PreToolUse": "*", "PostToolUse": "*", "PostToolUseFailure": "*", "UserPromptSubmit": ""}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for event, matcher := range want {
		if m, ok := got[event]; !ok || m != matcher {
			t.Errorf("%s: %q, %v; want %q", event, m, ok, matcher)
		}
	}
}
