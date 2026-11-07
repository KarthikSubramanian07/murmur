package join

import (
	"strings"
	"testing"
)

func envWith(vars map[string]string) Env {
	return Env{Getenv: func(k string) string { return vars[k] }}
}

// A Codex thread in T3 Code that existed before Murmur was added to Codex's config keeps lacking
// the tools, and T3 never refreshes it, so the agent is told to open a new thread, in these words
// (Tyler, 2026-10-30, from a Codex agent's measurements in T3).
func TestAdviceForACodexThreadInT3SaysToOpenANewThread(t *testing.T) {
	const want = "open a new thread in this project to talk to other agents through Murmur"
	for _, fresh := range []bool{true, false} {
		e := envWith(map[string]string{"CODEX_THREAD_ID": "019a", "T3CODE_CLI_PATH": "/opt/t3/bin/t3"})
		got := strings.Join(e.advice(false, fresh, false), "\n")
		if !strings.Contains(got, want) {
			t.Errorf("codexFresh %v: advice doesn't say %q:\n%s", fresh, want, got)
		}
		if strings.Contains(got, "claude --continue") || strings.Contains(got, "Stop in T3") {
			t.Errorf("codexFresh %v: advice for Codex tells about Claude:\n%s", fresh, got)
		}
	}
}

// Codex outside T3 has sessions, not threads, and the advice for it says so.
func TestAdviceForCodexOutsideT3SaysToStartANewSession(t *testing.T) {
	got := strings.Join(envWith(map[string]string{"CODEX_THREAD_ID": "019a"}).advice(false, true, false), "\n")
	if !strings.Contains(got, "start a new session") || strings.Contains(got, "open a new thread") {
		t.Errorf("advice:\n%s", got)
	}
}

// Claude Code in T3 still gets Claude's advice, though T3 is there too.
func TestAdviceForClaudeInT3IsNotCodexs(t *testing.T) {
	e := envWith(map[string]string{"CLAUDECODE": "1", "T3CODE_CLI_PATH": "/opt/t3/bin/t3"})
	got := strings.Join(e.advice(true, false, false), "\n")
	if !strings.Contains(got, "press Stop in T3") || strings.Contains(got, "open a new thread in this project to talk") {
		t.Errorf("advice:\n%s", got)
	}
}

// The paragraph about the check-in says what it sends, why, asks to leave it on, and how to turn it
// off; when it is already off, it says so and asks to turn it back on (#180).
func TestUsageAdviceSaysWhatIsSentAndAsksToKeepItOn(t *testing.T) {
	on := usageAdvice(false, "")
	for _, want := range []string{"murmur.afig.dev", "one-way hash", "no names, messages", "100", "Please ask your user to leave it on", "git config --global murmur.usage false"} {
		if !strings.Contains(on, want) {
			t.Errorf("on: doesn't say %q:\n%s", want, on)
		}
	}
	off := usageAdvice(true, "DO_NOT_TRACK is set")
	for _, want := range []string{"is off (DO_NOT_TRACK is set)", "sends nothing outside this repo", "100", "turn it back on"} {
		if !strings.Contains(off, want) {
			t.Errorf("off: doesn't say %q:\n%s", want, off)
		}
	}
}
