package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func runCount(t *testing.T, b *box) int {
	t.Helper()
	data, err := os.ReadFile(b.log)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "\n")
}

// sameFile reports whether two paths are one file; a temp directory can be reached by two names.
func sameFile(a, b string) bool {
	sa, errA := os.Stat(a)
	sb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(sa, sb)
}

func jsonAt(t *testing.T, path string, keys ...string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(readFile(t, path)), &v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

// init says the repo uses t4 in both files, and puts nothing else in the repo; join, run by an agent
// after its user agreed, does the rest on the user's side only. Running it again changes nothing,
// and --leave takes this repo's part back out.
func TestInitJoinAndLeave(t *testing.T) {
	b := newBox(t)
	b.extraEnv = []string{"CLAUDECODE=1"} // the agent running these is Claude Code

	b.mustRun(b.clone, "init")
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		if got := readFile(t, filepath.Join(b.clone, name)); !strings.Contains(got, "<!-- t4:start -->") || !strings.Contains(got, "t4 join") {
			t.Errorf("%s after init:\n%s", name, got)
		}
	}
	for _, name := range []string{".mcp.json", ".claude", "t4"} {
		if exists(filepath.Join(b.clone, name)) {
			t.Errorf("init wrote %s into the repo", name)
		}
	}
	if exists(b.claudeJSON()) || exists(b.codexConfig()) || exists(filepath.Join(b.clone, ".git", "t4")) {
		t.Fatal("init touched the user's settings or started something")
	}

	out := b.mustRun(b.clone, "join")
	if !strings.Contains(out, "Tell your user") || !strings.Contains(out, "claude --continue") {
		t.Errorf("join's output should tell the agent what to pass on, and that this conversation needs a restart:\n%s", out)
	}

	// Registered once for the user, with this binary's full path, and its tools allowed.
	server, _ := jsonAt(t, b.claudeJSON(), "mcpServers", "t4").(map[string]any)
	if cmd, _ := server["command"].(string); server == nil || !sameFile(cmd, b.t4) || !reflect.DeepEqual(server["args"], []any{"mcp"}) {
		t.Errorf("Claude Code's server: %v", server)
	}
	if allow, _ := jsonAt(t, b.claudeSettings(), "permissions", "allow").([]any); !reflect.DeepEqual(allow, []any{"mcp__t4"}) {
		t.Errorf("Claude Code's allow list: %v", allow)
	}
	toml := readFile(t, b.codexConfig())
	if !strings.Contains(toml, "[mcp_servers.t4]") || !strings.Contains(toml, `args = ["mcp", "--agent", "codex"]`) || !strings.Contains(toml, `default_tools_approval_mode = "approve"`) {
		t.Errorf("Codex's config:\n%s", toml)
	}

	// The hook is for this user in this repo, and not committed.
	hook := readFile(t, filepath.Join(b.clone, ".claude", "settings.local.json"))
	if !strings.Contains(hook, "PostToolUse") || !strings.Contains(hook, "UserPromptSubmit") || !strings.Contains(hook, ` hook`) {
		t.Errorf("settings.local.json:\n%s", hook)
	}
	status := git(t, b.clone, "status", "--porcelain")
	if strings.Contains(status, ".claude") || strings.Contains(status, ".mcp.json") {
		t.Errorf("join left files that a commit would pick up:\n%s", status)
	}
	if !exists(filepath.Join(b.clone, ".git", "t4", "joined.json")) || !exists(filepath.Join(b.clone, ".git", "t4", "daemon.json")) {
		t.Error("join didn't record consent and start the daemon")
	}

	// Again: nothing changes, and claude isn't asked anything.
	before, runs := snapshot(t, b.home, b.clone), runCount(t, b)
	out = b.mustRun(b.clone, "join")
	if !strings.Contains(out, "Nothing to do") {
		t.Errorf("joining again should say so:\n%s", out)
	}
	if after := snapshot(t, b.home, b.clone); !reflect.DeepEqual(before, after) || runCount(t, b) != runs {
		t.Error("joining twice changed something")
	}
	// Now the server is registered, a session that started after that has it already.
	if !strings.Contains(out, "within a few seconds") {
		t.Errorf("a repeat join in Claude Code should say the tools are coming:\n%s", out)
	}

	// --leave takes this repo's part out: the consent, the hook, the daemon. The per-user
	// registration stays, as other repos may have joined.
	out = b.mustRun(b.clone, "join", "--leave")
	if !strings.Contains(out, "Removed the record") {
		t.Errorf("leave's output:\n%s", out)
	}
	if exists(filepath.Join(b.clone, ".git", "t4", "joined.json")) || exists(filepath.Join(b.clone, ".git", "t4", "daemon.json")) {
		t.Error("consent or the daemon survived --leave")
	}
	if exists(filepath.Join(b.clone, ".claude", "settings.local.json")) || exists(filepath.Join(b.clone, ".claude")) {
		t.Error("the hook survived --leave")
	}
	if jsonAt(t, b.claudeJSON(), "mcpServers", "t4") == nil || !strings.Contains(readFile(t, b.codexConfig()), "[mcp_servers.t4]") {
		t.Error("--leave unregistered the server for every repo")
	}
	if !strings.Contains(readFile(t, filepath.Join(b.clone, "AGENTS.md")), "<!-- t4:start -->") {
		t.Error("--leave touched the repo's files")
	}
	if out = b.mustRun(b.clone, "join", "--leave"); !strings.Contains(out, "Nothing to undo") {
		t.Errorf("leaving twice should say so:\n%s", out)
	}

	// And back in.
	b.mustRun(b.clone, "join")
	if !exists(filepath.Join(b.clone, ".git", "t4", "joined.json")) || !exists(filepath.Join(b.clone, ".claude", "settings.local.json")) {
		t.Error("joining again after leaving didn't join")
	}
}

// What the user already had in Claude Code's and Codex's files stays as it was, in its order.
func TestJoinKeepsTheUsersConfig(t *testing.T) {
	b := newBox(t)
	b.mustRun(b.clone, "init")
	writeFile(t, b.claudeJSON(), "{\n  \"numStartups\": 7,\n  \"mcpServers\": {\n    \"other\": {\"type\": \"stdio\", \"command\": \"o\", \"args\": []}\n  },\n  \"theme\": \"dark\"\n}\n")
	writeFile(t, b.claudeSettings(), "{\n  \"model\": \"x\",\n  \"permissions\": {\"allow\": [\"Bash(ls)\"]}\n}\n")
	codex := "# my codex\nmodel = \"gpt\"\n\n[mcp_servers.other]\ncommand = \"o\"  # mine\n"
	writeFile(t, b.codexConfig(), codex)
	writeFile(t, filepath.Join(b.clone, ".claude", "settings.local.json"), "{\n  \"permissions\": {\"allow\": [\"Bash(git status)\"]}\n}\n")

	b.mustRun(b.clone, "join")

	cfg := readFile(t, b.claudeJSON())
	if !(strings.Index(cfg, "numStartups") < strings.Index(cfg, "mcpServers") && strings.Index(cfg, `"other"`) < strings.Index(cfg, `"t4"`) && strings.Index(cfg, `"t4"`) < strings.Index(cfg, "theme")) {
		t.Errorf(".claude.json lost the order of the user's keys:\n%s", cfg)
	}
	if allow, _ := jsonAt(t, b.claudeSettings(), "permissions", "allow").([]any); !reflect.DeepEqual(allow, []any{"Bash(ls)", "mcp__t4"}) {
		t.Errorf("allow list: %v", allow)
	}
	if got := readFile(t, b.codexConfig()); !strings.HasPrefix(got, codex) {
		t.Errorf("config.toml no longer starts with what the user wrote:\n%s", got)
	}
	local := readFile(t, filepath.Join(b.clone, ".claude", "settings.local.json"))
	if !strings.Contains(local, "Bash(git status)") || !strings.Contains(local, "PostToolUse") {
		t.Errorf("settings.local.json:\n%s", local)
	}
	b.mustRun(b.clone, "join", "--leave")
	local = readFile(t, filepath.Join(b.clone, ".claude", "settings.local.json"))
	if !strings.Contains(local, "Bash(git status)") || strings.Contains(local, "hook") {
		t.Errorf("settings.local.json after --leave:\n%s", local)
	}
}

// join does nothing in a repo that doesn't say it uses t4: the user's yes was to this repo's invitation.
func TestJoinNeedsTheMarker(t *testing.T) {
	b := newBox(t)
	out, code := b.run(b.clone, "join")
	if code == 0 || !strings.Contains(out, "doesn't say it uses t4") {
		t.Errorf("exit %d:\n%s", code, out)
	}
	if exists(b.claudeJSON()) || exists(b.codexConfig()) || exists(filepath.Join(b.clone, ".git", "t4")) || exists(filepath.Join(b.clone, ".claude")) {
		t.Error("join changed something in a repo that doesn't use t4")
	}
}

// The commands for people are gone, and the help doesn't list them: agents message through MCP,
// people through the page.
func TestTheMessagingCommandsAreGone(t *testing.T) {
	b := newBox(t)
	for _, cmd := range []string{"who", "send", "inbox", "log"} {
		out, code := b.run(b.clone, cmd)
		if code != 2 || !strings.Contains(out, "unknown command") {
			t.Errorf("t4 %s: exit %d, want 2 and an unknown command\n%s", cmd, code, out)
		}
	}
	help := b.mustRun(b.clone, "help")
	for _, gone := range []string{"t4 who", "t4 send", "t4 inbox", "t4 log"} {
		if strings.Contains(help, gone) {
			t.Errorf("help still lists %q:\n%s", gone, help)
		}
	}
	for _, kept := range []string{"t4 init", "t4 join", "t4 ui", "t4 stop"} {
		if !strings.Contains(help, kept) {
			t.Errorf("help doesn't list %q:\n%s", kept, help)
		}
	}
	for _, kept := range []string{"version", "stop"} {
		if _, code := b.run(b.clone, kept); code != 0 {
			t.Errorf("t4 %s: exit %d", kept, code)
		}
	}
}

// A daemon publishes this machine's presence to the team, which only a user who joined agreed to,
// so one started in a clone that hasn't joined exits.
func TestDaemonWillNotRunInAClonethatHasNotJoined(t *testing.T) {
	b := newBox(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, b.t4, "daemon", "-C", b.clone)
	cmd.Env = b.env()
	out, err := cmd.CombinedOutput()
	if err == nil || ctx.Err() != nil || !strings.Contains(string(out), "hasn't joined") {
		t.Errorf("the daemon ran (err %v, timed out %v):\n%s", err, ctx.Err() != nil, out)
	}
	if exists(filepath.Join(b.clone, ".git", "t4", "daemon.json")) {
		t.Error("it left a daemon.json")
	}
}

// remoteRefs lists the t4 refs on the remote, where teammates would see them.
func remoteRefs(t *testing.T, b *box) string {
	t.Helper()
	return strings.TrimSpace(git(t, b.remote, "for-each-ref", "refs/t4"))
}

// mcpSession starts `t4 mcp` in the clone, as an agent's client does, and connects to it.
func (b *box) mcpSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.Command(b.t4, "mcp", "-C", b.clone)
	cmd.Env = append(b.env(), b.extraEnv...)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close(); cancel() })
	return cs
}

func tools(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(30 * time.Second); !cond(); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func quiet(t *testing.T, b *box, what string) {
	t.Helper()
	time.Sleep(3 * time.Second) // longer than it waits between looks, and than a daemon takes to start
	if exists(filepath.Join(b.clone, ".git", "t4")) {
		t.Errorf("%s: a daemon's directory exists", what)
	}
	if refs := strings.TrimSpace(git(t, b.clone, "for-each-ref", "refs/t4")) + remoteRefs(t, b); refs != "" {
		t.Errorf("%s: t4 refs exist: %s", what, refs)
	}
}

// The user-scope server starts in every repo the user opens. Where the repo hasn't invited t4, or
// this user hasn't joined, it has no tools and no instructions, starts no daemon and writes no ref,
// locally or on the remote.
func TestTheMCPServerIsPassiveWhereItIsNotInvited(t *testing.T) {
	b := newBox(t)

	// A repo that doesn't use t4.
	cs := b.mcpSession(t)
	if names := tools(t, cs); len(names) != 0 {
		t.Errorf("tools in a repo that doesn't use t4: %q", names)
	}
	quiet(t, b, "no marker")
	cs.Close()

	// A repo that does, where this user hasn't joined.
	b.mustRun(b.clone, "init")
	cs = b.mcpSession(t)
	if names := tools(t, cs); len(names) != 0 {
		t.Errorf("tools before joining: %q", names)
	}
	if cs.InitializeResult().Instructions != "" {
		t.Error("instructions before joining")
	}
	quiet(t, b, "marker, not joined")
}

// Joining while a session is open turns its server on, and leaving turns it off again, without
// restarting the daemon that --leave stopped.
func TestTheMCPServerFollowsJoinAndLeave(t *testing.T) {
	b := newBox(t)
	b.mustRun(b.clone, "init")
	cs := b.mcpSession(t)
	if names := tools(t, cs); len(names) != 0 {
		t.Fatalf("tools before joining: %q", names)
	}

	b.mustRun(b.clone, "join")
	eventually(t, "the tools to appear", func() bool { return len(tools(t, cs)) == 4 })
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "who"})
	if err != nil || res.IsError {
		t.Fatalf("who after joining: %v %+v", err, res)
	}

	b.mustRun(b.clone, "join", "--leave")
	eventually(t, "the tools to go", func() bool { return len(tools(t, cs)) == 0 })
	time.Sleep(3 * time.Second)
	if exists(filepath.Join(b.clone, ".git", "t4", "daemon.json")) {
		t.Error("a daemon is running after --leave")
	}
}
