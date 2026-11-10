package join

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/jsonedit"
)

// The hash join writes is the one Codex computes: these are the trust entries Codex 0.166.0 wrote
// itself, on "Trust all and continue", for these four hooks (2026-11-08, a throwaway CODEX_HOME).
func TestCodexHookHashIsCodexs(t *testing.T) {
	const cmd = `"C:/Users/smash/.local/bin/mur.exe" hook`
	for _, c := range []struct{ event, matcher, want string }{
		{"pre_tool_use", "*", "sha256:8d133cfe2aa574aff87e04e0af49b96c8379b6bad07dd2b620a34b04712be37e"},
		{"post_tool_use", "*", "sha256:6e1bc859b4be6ea1475e44ba8b7a48738ae1e7f7f9b6e61cc5fcd194587b49d2"},
		{"user_prompt_submit", "", "sha256:4733ba395e0a1b7a607a72a4e6e53030287d5b43415d1bb0826ea52ac40281b8"},
		{"stop", "", "sha256:aee4b345660f420d26b523d2ea296c25778a6a43f969c00fffbc0e266e448bab"},
	} {
		got, err := codexHandlerHash(c.event, c.matcher, cmd, 600, "")
		if err != nil || got != c.want {
			t.Errorf("%s: %s %v, want %s", c.event, got, err, c.want)
		}
	}
}

// hooks.json keeps the user's own hooks, in their order, and Murmur's goes last in each event it
// uses, once, however often join runs. The trust keys name each group's place.
func TestEditCodexHooksKeepsOthersAndIsIdempotent(t *testing.T) {
	const path = `C:\home\.codex\hooks.json`
	mine := "{\n    \"other\": 1,\n    \"hooks\": {\"PostToolUse\": [{\"matcher\": \"Bash\", \"hooks\": [{\"type\": \"command\", \"command\": \"lint\"}]}]}\n}\n"
	out, trust, changed, err := editCodexHooks([]byte(mine), path, "mur hook codex")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if !strings.HasPrefix(string(out), "{\n    \"other\": 1,\n    \"hooks\"") {
		t.Errorf("the key order or indent changed:\n%s", out)
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string           `json:"matcher"`
			Hooks   []map[string]any `json:"hooks"`
		} `json:"hooks"`
		Other int `json:"other"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	post := doc.Hooks["PostToolUse"]
	if doc.Other != 1 || len(post) != 2 || post[0].Hooks[0]["command"] != "lint" || post[1].Matcher != "*" ||
		post[1].Hooks[0]["statusMessage"] != hookName || len(doc.Hooks["Stop"]) != 1 || len(doc.Hooks["UserPromptSubmit"]) != 1 {
		t.Errorf("hooks.json:\n%s", out)
	}
	keys := map[string]bool{}
	for _, tr := range trust {
		keys[tr.key] = true
	}
	for _, k := range []string{path + ":post_tool_use:1:0", path + ":stop:0:0", path + ":user_prompt_submit:0:0"} {
		if !keys[k] {
			t.Errorf("no trust for %s: %v", k, trust)
		}
	}
	again, trust2, changed, err := editCodexHooks(out, path, "mur hook codex")
	if err != nil || changed || string(again) != string(out) || len(trust2) != len(trust) || trust2[0] != trust[0] {
		t.Errorf("second run: changed=%v err=%v", changed, err)
	}
	// A new command (mur moved) replaces the old entry in its place rather than adding one, and a
	// hook the user added after it keeps its place, and so its trust.
	var later map[string]any
	json.Unmarshal(out, &later)
	h := later["hooks"].(map[string]any)
	h["Stop"] = append(h["Stop"].([]any), map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "notify"}}})
	withLater, _ := json.Marshal(later)
	moved, trust3, changed, _ := editCodexHooks(withLater, path, "mur2 hook codex")
	if !changed || strings.Count(string(moved), "hook codex") != 3 || strings.Contains(string(moved), `"mur hook codex"`) {
		t.Errorf("after a move:\n%s", moved)
	}
	json.Unmarshal(moved, &later)
	stop := later["hooks"].(map[string]any)["Stop"].([]any)
	if len(stop) != 2 || !strings.Contains(fmt.Sprint(stop[1]), "notify") {
		t.Errorf("Stop after a move: %v", stop)
	}
	if len(trust3) != 3 || trust3[0].key != path+":user_prompt_submit:0:0" {
		t.Errorf("trust after a move: %v", trust3)
	}
}

// A group join didn't write isn't taken for Murmur's, and the Claude Code hook's form, which
// doesn't run in Codex on Windows, is replaced where it is.
func TestIsMurmurGroup(t *testing.T) {
	group := func(cmd, status string) any {
		h := jsonedit.New()
		h.Set("command", cmd)
		if status != "" {
			h.Set("statusMessage", status)
		}
		g := jsonedit.New()
		g.Set("hooks", []any{h})
		return g
	}
	for _, c := range []struct {
		cmd, status string
		want        bool
	}{
		{`C:\bin\mur.exe hook codex`, "", true},
		{`& 'C:\a b\mur.exe' hook codex`, "", true},
		{`'/usr/local/bin/mur' hook grok`, "", true},
		{`"C:/Users/x/mur.exe" hook`, "", true},
		{"lint", hookName, true},
		{"lint", "", false},
		{`C:\bin\other.exe hook codex`, "", false},
	} {
		if got := isMurmurGroup(group(c.cmd, c.status)); got != c.want {
			t.Errorf("%q %q: %v", c.cmd, c.status, got)
		}
	}
}

// What join can't read it leaves alone, and says so.
func TestEditCodexHooksRefusesWhatItCantRead(t *testing.T) {
	for _, text := range []string{`{not json`, `{"hooks":[]}`, `{"hooks":{"Stop":{}}}`} {
		if _, _, _, err := editCodexHooks([]byte(text), "h.json", "mur hook codex"); err == nil {
			t.Errorf("%s: no error", text)
		}
	}
}

// Trust goes in as Codex writes it: a [hooks.state.'<key>'] table per hook. A table that is there
// gets its hash replaced in place, and every other line stays.
func TestEditCodexTrust(t *testing.T) {
	trust := []codexTrust{{`C:\h\hooks.json:stop:0:0`, "sha256:new"}}
	before := "model = \"gpt\"\n\n[hooks.state]\n\n[hooks.state.'C:\\h\\hooks.json:stop:0:0']\ntrusted_hash = \"sha256:old\" # mine\n\n[tui]\nx = 1\n"
	out, changed, err := editCodexTrust(before, trust)
	want := strings.Replace(before, `"sha256:old" # mine`, `"sha256:new"`, 1)
	if err != nil || !changed || out != want {
		t.Errorf("replace: changed=%v err=%v\n%s", changed, err, out)
	}
	if again, changed, _ := editCodexTrust(out, trust); changed || again != out {
		t.Errorf("second run changed it")
	}
	out, _, err = editCodexTrust("model = \"gpt\"\r\n", trust)
	if err != nil || out != "model = \"gpt\"\r\n\r\n[hooks.state.'C:\\h\\hooks.json:stop:0:0']\r\ntrusted_hash = \"sha256:new\"\r\n" {
		t.Errorf("append, CRLF: %v %q", err, out)
	}
	header := "[hooks.state.'C:\\h\\hooks.json:stop:0:0']\n\n[tui]\n"
	if out, _, err := editCodexTrust(header, trust); err != nil || !strings.HasPrefix(out, "[hooks.state.'C:\\h\\hooks.json:stop:0:0']\ntrusted_hash = \"sha256:new\"\n") {
		t.Errorf("a table without the hash: %v\n%s", err, out)
	}
	// Other keys of the hook's table stay, and a header inside a multi-line string is no header.
	other := "[hooks.state.'C:\\h\\hooks.json:stop:0:0']\nenabled = true\nnote = '''\n[tui]\n'''\n\n[tui]\n"
	if out, _, err := editCodexTrust(other, trust); err != nil || out != strings.Replace(other, "'''\n\n", "'''\ntrusted_hash = \"sha256:new\"\n\n", 1) {
		t.Errorf("another key of the table: %v\n%s", err, out)
	}
	// Quotes in a comment or a one-line string open nothing.
	commented := "[hooks.state.'C:\\h\\hooks.json:stop:0:0']\nnote = \"'''\" # '''\n# '''\ntrusted_hash = \"sha256:old\"\n"
	if out, _, err := editCodexTrust(commented, trust); err != nil || out != strings.Replace(commented, "sha256:old", "sha256:new", 1) {
		t.Errorf("quotes in a comment: %v\n%s", err, out)
	}
	for _, c := range []struct{ line, open, want string }{
		{`a = """x`, "", `"""`},
		{`x""" # '''`, `"""`, ""},
		{`x\""" still`, `"""`, `"""`},
		{`a = '''x'''`, "", ""},
		{`a = "\"'''"`, "", ""},
		{`'''' b`, "'''", ""},
	} {
		if got := tomlStringAfter(c.line, c.open); got != c.want {
			t.Errorf("tomlStringAfter(%q, %q) = %q, want %q", c.line, c.open, got, c.want)
		}
	}
	for _, bad := range []string{
		"[hooks.state.'C:\\h\\hooks.json:stop:0:0']\ntrusted_hash = \"\"\"\nsha256:old\"\"\"\n",
		"[hooks.state]\n'C:\\h\\hooks.json:stop:0:0'.trusted_hash = \"x\"\n",
		"[hooks]\nstate = {}\n",
		"hooks.state = {}\n",
	} {
		if _, _, err := editCodexTrust(bad, trust); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

// Codex canonicalizes a home CODEX_HOME names, and takes the default ~/.codex as it is. With TEMP
// a short name (CI's Windows runner has C:\Users\RUNNER~1), the two differ even with no link
// (symlink_test.go has the link).
func TestCodexKeyPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "hooks.json")
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := codexKeyPath(path, true); got != filepath.Join(resolved, "hooks.json") {
		t.Errorf("named: %s", got)
	}
	if got := codexKeyPath(path, false); got != path {
		t.Errorf("default: %s", got)
	}
}

// The command runs in whatever shell the agent uses: on Windows that rules out a quoted path
// followed by arguments, which PowerShell doesn't run, and a forward slash, which cmd takes for a
// switch.
func TestAgentHookCommand(t *testing.T) {
	got := agentHookCommand(`C:\Users\me\.local\bin\mur.exe`, "codex")
	want := `'C:\Users\me\.local\bin\mur.exe' hook codex`
	if runtime.GOOS == "windows" {
		want = `C:\Users\me\.local\bin\mur.exe hook codex`
	}
	if got != want {
		t.Errorf("command %q, want %q", got, want)
	}
	if runtime.GOOS == "windows" {
		// A path no short name can make safe gets PowerShell's call operator.
		if got := agentHookCommand(`Z:\no such\o'k\mur.exe`, "grok"); got != `& 'Z:\no such\o''k\mur.exe' hook grok` {
			t.Errorf("command %q", got)
		}
	}
	// Each of PowerShell's single quotes is doubled, the curly ones too.
	if got := psQuote("a'b\u2018c\u2019d\u201ae\u201bf"); got != "'a''b\u2018\u2018c\u2019\u2019d\u201a\u201ae\u201b\u201bf'" {
		t.Errorf("psQuote %q", got)
	}
	if !bareSafe(`C:\Users\me\.local\bin\mur.exe`) || bareSafe(`C:\a b\mur.exe`) || bareSafe(`C:\a&b\mur.exe`) || bareSafe(`C:/a/mur.exe`) {
		t.Error("bareSafe")
	}
}
