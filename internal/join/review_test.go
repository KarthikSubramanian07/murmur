package join

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for what Codex's review of #136 found (2026-11-01).

// With both opencode.json and opencode.jsonc, the .jsonc, which OpenCode reads last, gets the
// server and the rule, and an entry in the .json is made the same, so that its keys can't override
// join's when OpenCode merges the two.
func TestRegisterOpenCodeWithBothFiles(t *testing.T) {
	sb := newSandbox(t, false)
	withAgents(sb)
	withOpenCode(t, sb, "opencode v2.2.0")
	jsonPath, jsoncPath := filepath.Join(sb.OpenCodeDir, "opencode.json"), filepath.Join(sb.OpenCodeDir, "opencode.jsonc")
	write(t, jsonPath, `{"mcp": {"servers": {"murmur": {"type": "local", "command": ["/old/mur"], "disabled": true}}}}`)
	write(t, jsoncPath, "{\n  // mine\n}\n")
	if _, _, err := sb.registerOpenCodeAt(context.Background(), sb.defaultHome(agents[agentIndex("opencode")])); err != nil {
		t.Fatal(err)
	}
	jsonc := read(t, jsoncPath)
	if !strings.Contains(jsonc, "// mine") || !strings.Contains(jsonc, `"codemode": false`) || !strings.Contains(jsonc, `"murmur_*"`) {
		t.Errorf("opencode.jsonc:\n%s", jsonc)
	}
	if got := read(t, jsonPath); strings.Contains(got, "disabled") || strings.Contains(got, "/old/mur") {
		t.Errorf("opencode.json keeps the stale entry:\n%s", got)
	}
	// Leaving says to take it out of both.
	if r := openCodeRemoval(sb.Env, sb.defaultHome(agents[agentIndex("opencode")])); !strings.Contains(r, jsonPath) || !strings.Contains(r, jsoncPath) {
		t.Errorf("removal: %q", r)
	}
}

// A rule of the user's after Murmur's, which would override it, puts Murmur's last again.
func TestRegisterOpenCodeKeepsTheRuleLast(t *testing.T) {
	sb := newSandbox(t, false)
	withAgents(sb)
	withOpenCode(t, sb, "opencode v2.2.0")
	path := filepath.Join(sb.OpenCodeDir, "opencode.json")
	write(t, path, `{"permissions": [{"action": "murmur_*", "resource": "*", "effect": "allow"}, {"action": "*", "resource": "*", "effect": "ask"}]}`)
	h := sb.defaultHome(agents[agentIndex("opencode")])
	if _, _, err := sb.registerOpenCodeAt(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if strings.Count(got, `"murmur_*"`) != 1 || strings.LastIndex(got, `"murmur_*"`) < strings.Index(got, `"ask"`) {
		t.Errorf("opencode.json:\n%s", got)
	}
}

// An instance T3 runs with no directory of its own uses the user's default, not the directory of the
// instance that ran join, which may be another account's.
func TestT3InstanceWithoutOverrideGetsTheUsersDefault(t *testing.T) {
	sb := newSandbox(t, false)
	withAgents(sb) // the agent running join has XDG_CONFIG_HOME = <root>/config
	sb.Home = sb.root
	sb.T3Settings = filepath.Join(sb.root, "settings.json")
	write(t, sb.T3Settings, `{"providerInstances": {"muse": {"driver": "muse", "enabled": true}, "work": {"driver": "opencode", "enabled": true}}}`)
	byAgent, _ := sb.homes()
	for driver, want := range map[string]string{"muse": filepath.Join(sb.root, ".config", "muse"), "opencode": filepath.Join(sb.root, ".config", "opencode")} {
		hs := byAgent[agentIndex(driver)]
		if len(hs) != 2 || !samePath(hs[1].Dir, want) {
			t.Errorf("%s homes %+v, want the default and %s", driver, hs, want)
		}
	}
}

// Pi reads ~ in PI_CODING_AGENT_DIR as the home directory, and so does join.
func TestPiDirWithATilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", "~/.pi-work")
	e, err := EnvFromProcess()
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(e.PiDir, filepath.Join(home, ".pi-work")) {
		t.Errorf("PiDir %q", e.PiDir)
	}
}

// A server named murmur in Pi's mcp.json, which Pi would run instead of the extension's, is reported.
func TestRegisterPiReportsAServerInMCPJSON(t *testing.T) {
	sb := newSandbox(t, false)
	withAgents(sb)
	mcp := `{"mcpServers": {"murmur": {"command": "/old/mur", "args": ["mcp"]}}}`
	write(t, filepath.Join(sb.PiDir, "mcp.json"), mcp)
	h := sb.defaultHome(agents[agentIndex("pi")])
	for range 2 { // on the run that writes the extension, and on the one after
		if _, _, err := sb.registerPiAt(h); err == nil || !strings.Contains(err.Error(), "mcp.json") {
			t.Errorf("err %v", err)
		}
	}
	if read(t, filepath.Join(sb.PiDir, "mcp.json")) != mcp {
		t.Error("changed the user's mcp.json")
	}
}
