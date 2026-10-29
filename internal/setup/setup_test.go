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
