package join

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withAgents gives the sandbox the other agents' directories, under its root, with none of them
// there yet.
func withAgents(sb *sandbox) {
	sb.OpenCodeDir = filepath.Join(sb.root, "config", "opencode")
	sb.MuseDir = filepath.Join(sb.root, "config", "muse")
	sb.PiDir = filepath.Join(sb.root, "pi", "agent")
	sb.GrokHome = filepath.Join(sb.root, "grok")
}

// OpenCode's JSONC config gets the server and the allow rule, its comments and layout kept; once.
func TestRegisterOpenCodeKeepsTheConfig(t *testing.T) {
	sb := newSandbox(t, false)
	withAgents(sb)
	path := filepath.Join(sb.OpenCodeDir, "opencode.json")
	before := "{\n    // mine\n    \"$schema\":  \"https://opencode.ai/config.json\",\n    \"model\":  \"x/y\",\n}\n"
	write(t, path, before)
	h := sb.defaultHome(agents[agentIndex("opencode")])
	did, fresh, err := sb.registerOpenCodeAt(h)
	if err != nil || !fresh || len(did) != 2 {
		t.Fatalf("did=%q fresh=%v err=%v", did, fresh, err)
	}
	got := read(t, path)
	for _, want := range []string{"// mine", `"$schema":  "https://opencode.ai/config.json",`, `"type": "local"`, `"codemode": false`, `"--agent"`, `"opencode"`, `"action": "murmur_*"`} {
		if !strings.Contains(got, want) {
			t.Errorf("opencode.json lacks %s:\n%s", want, got)
		}
	}
	if _, fresh, err := sb.registerOpenCodeAt(h); err != nil || fresh || read(t, path) != got {
		t.Errorf("second: fresh=%v err=%v", fresh, err)
	}
	if r := openCodeRemoval(sb.Env, h); !strings.Contains(r, path) {
		t.Errorf("removal: %q", r)
	}
}

// A machine without OpenCode, or Muse, Pi or Grok, gets none of their files.
func TestOtherAgentsSkipAMachineWithoutThem(t *testing.T) {
	sb := newSandbox(t, false)
	withAgents(sb)
	for _, d := range []string{"opencode", "muse", "pi", "grok"} {
		a := agents[agentIndex(d)]
		did, fresh, err := a.register(sb.Env, context.Background(), sb.defaultHome(a))
		if err != nil || fresh || len(did) != 0 {
			t.Errorf("%s: did=%q fresh=%v err=%v", d, did, fresh, err)
		}
	}
	for _, dir := range []string{sb.OpenCodeDir, sb.MuseDir, sb.PiDir, sb.GrokHome} {
		if exists(dir) {
			t.Errorf("created %s", dir)
		}
	}
}

func TestRegisterOpenCodeRefusesWhatItCannotEdit(t *testing.T) {
	sb := newSandbox(t, false)
	withAgents(sb)
	path := filepath.Join(sb.OpenCodeDir, "opencode.jsonc")
	bad := "{\"mcp\": [], // a list\n}"
	write(t, path, bad)
	if _, _, err := sb.registerOpenCodeAt(sb.defaultHome(agents[agentIndex("opencode")])); err == nil {
		t.Error("no error")
	}
	if read(t, path) != bad {
		t.Error("changed a file it couldn't edit")
	}
}

func TestRegisterMuse(t *testing.T) {
	sb := newSandbox(t, false)
	withAgents(sb)
	path := filepath.Join(sb.MuseDir, "settings.json")
	write(t, path, "{\n  \"schema_version\": 1,\n  \"tui\": {\n    \"foreign_context_notice_shown\": true\n  }\n}\n")
	h := sb.defaultHome(agents[agentIndex("muse")])
	if _, fresh, err := sb.registerMuseAt(h); err != nil || !fresh {
		t.Fatalf("fresh=%v err=%v", fresh, err)
	}
	got := read(t, path)
	for _, want := range []string{`"foreign_context_notice_shown": true`, `"mcpServers": {`, `"mode": "optional"`, `"T3CODE_CLI_PATH": "${T3CODE_CLI_PATH:-}"`, `"muse"`} {
		if !strings.Contains(got, want) {
			t.Errorf("settings.json lacks %s:\n%s", want, got)
		}
	}
	if _, fresh, _ := sb.registerMuseAt(h); fresh {
		t.Error("registered twice")
	}

	// The older key, beside which a new one would lose every server, is left for the user.
	legacy := `{"schema_version": 1, "mcp_servers": {"x": {"command": "x"}}}`
	write(t, path, legacy)
	if _, _, err := sb.registerMuseAt(h); err == nil || read(t, path) != legacy {
		t.Errorf("a file with mcp_servers: err %v\n%s", err, read(t, path))
	}
}

// Pi gets Murmur's extension, which names this binary, once; a file of the same name that isn't
// Murmur's is left alone.
func TestRegisterPi(t *testing.T) {
	sb := newSandbox(t, false)
	withAgents(sb)
	if err := os.MkdirAll(sb.PiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	h := sb.defaultHome(agents[agentIndex("pi")])
	if _, fresh, err := sb.registerPiAt(h); err != nil || !fresh {
		t.Fatalf("fresh=%v err=%v", fresh, err)
	}
	path := piExtensionPath(sb.PiDir)
	got := read(t, path)
	for _, want := range []string{`pi.registerMcpServer("murmur"`, `MURMUR_PI_SESSION_FILE`, `"--agent", "pi"`, `exposure: "direct"`, `"session_start"`} {
		if !strings.Contains(got, want) {
			t.Errorf("the extension lacks %s:\n%s", want, got)
		}
	}
	if !strings.Contains(strings.ReplaceAll(got, `\\`, `\`), sb.Exe) {
		t.Errorf("the extension doesn't run %s:\n%s", sb.Exe, got)
	}
	if _, fresh, _ := sb.registerPiAt(h); fresh {
		t.Error("wrote it twice")
	}
	write(t, path, "export default function () {}\n")
	if _, _, err := sb.registerPiAt(h); err == nil {
		t.Error("replaced someone else's file")
	}
}

func TestRegisterGrok(t *testing.T) {
	sb := newSandbox(t, false)
	withAgents(sb)
	path := filepath.Join(sb.GrokHome, "config.toml")
	before := "# mine\n[permission]\nallow = [\"Bash(ls)\"] # kept\n"
	write(t, path, before)
	h := sb.defaultHome(agents[agentIndex("grok")])
	did, fresh, err := sb.registerGrokAt(h)
	if err != nil || !fresh || len(did) != 3 {
		t.Fatalf("did=%q fresh=%v err=%v", did, fresh, err)
	}
	hooks := read(t, filepath.Join(sb.GrokHome, "hooks", "murmur.json"))
	for _, want := range []string{`"PostToolUse"`, `"Stop"`, ` hook grok"`, `"timeout": 30`} {
		if !strings.Contains(hooks, want) {
			t.Errorf("the hook file lacks %s:\n%s", want, hooks)
		}
	}
	if strings.Contains(hooks, "matcher") || strings.Contains(hooks, "UserPromptSubmit") {
		t.Errorf("the hook file:\n%s", hooks)
	}
	got := read(t, path)
	want := "# mine\n[permission]\nallow = [\"Bash(ls)\", \"MCPTool(murmur__*)\"] # kept\n\n[mcp_servers.murmur]\ncommand = " + tomlString(sb.Exe) + "\nargs = [\"mcp\", \"--agent\", \"grok\"]\n"
	if got != want {
		t.Errorf("config.toml:\n%s\nwant:\n%s", got, want)
	}
	if did, fresh, err := sb.registerGrokAt(h); err != nil || fresh || len(did) != 0 {
		t.Errorf("second: did=%q fresh=%v err=%v", did, fresh, err)
	}
	if r := grokRemoval(sb.Env, h); !strings.Contains(r, path) || !strings.Contains(r, "murmur.json") {
		t.Errorf("removal: %q", r)
	}
}

func TestAddTOMLListItem(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"", "[permission]\nallow = [\"x\"]\n"},
		{"a = 1\n", "a = 1\n\n[permission]\nallow = [\"x\"]\n"},
		{"[permission]\nmode = \"y\"\n\n[other]\n", "[permission]\nmode = \"y\"\nallow = [\"x\"]\n\n[other]\n"},
		{"[permission]\r\nallow = []\r\n", "[permission]\r\nallow = [\"x\"]\r\n"},
	} {
		got, changed, err := addTOMLListItem(tc.src, "permission", "allow", "x", "config.toml")
		if err != nil || !changed || got != tc.want {
			t.Errorf("%q: got %q (%v, %v), want %q", tc.src, got, changed, err, tc.want)
		}
	}
	for _, bad := range []string{"permission.allow = [\"y\"]\n", "permission = {allow = []}\n", "[permission]\nallow = [\n  \"y\",\n]\n"} {
		if _, _, err := addTOMLListItem(bad, "permission", "allow", "x", "config.toml"); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

// What join prints after adding Murmur to the other agents says when their sessions get it.
func TestOthersAdvice(t *testing.T) {
	got := othersAdvice([]string{"OpenCode", "Muse Code"})
	if !strings.Contains(got, "OpenCode sessions get its tools within seconds") || !strings.Contains(got, "Muse Code loads MCP servers only when a session starts") {
		t.Errorf("advice: %s", got)
	}
}
