package join

import (
	"strings"
	"testing"
)

const tomlTable = "[mcp_servers.t4]\ncommand = \"/opt/t4\"\nargs = [\"mcp\", \"--agent\", \"codex\"]\ndefault_tools_approval_mode = \"approve\"\n"

func edit(t *testing.T, text string) (string, bool) {
	t.Helper()
	out, changed, err := editCodexConfig(text, "/opt/t4")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	return out, changed
}

// No config yet: the file is just the server's table.
func TestCodexEditCreatesTheTable(t *testing.T) {
	out, changed := edit(t, "")
	if !changed || out != tomlTable {
		t.Errorf("changed=%v:\n%s", changed, out)
	}
}

// Everything the user wrote stays exactly as it was, comments and odd spacing included, and the
// table goes at the end.
func TestCodexEditKeepsTheRestOfTheFile(t *testing.T) {
	before := "# my codex\nmodel   =  \"x\"\n\n[mcp_servers.other]\ncommand = \"o\"   # keep\nargs=['a']\n\n[profiles.p]\nmodel = \"y\"\n"
	out, changed := edit(t, before)
	if want := before + "\n" + tomlTable; !changed || out != want {
		t.Errorf("changed=%v:\n%s\nwant:\n%s", changed, out, want)
	}
	// No newline at the end of the file.
	out, _ = edit(t, strings.TrimSuffix(before, "\n"))
	if want := before + "\n" + tomlTable; out != want {
		t.Errorf("without a final newline:\n%s\nwant:\n%s", out, want)
	}
}

// Joining again, with the same binary, changes nothing; with the binary somewhere else, it
// changes the lines that name it and keeps the user's own settings in the table.
func TestCodexEditIsIdempotentAndUpdatesInPlace(t *testing.T) {
	out, _ := edit(t, "model = \"x\"\n")
	if again, changed := edit(t, out); changed || again != out {
		t.Errorf("a second edit changed the file (changed=%v):\n%s", changed, again)
	}

	stale := "[mcp_servers.t4]\n# mine\ncommand = '/old/t4'\nstartup_timeout_sec = 30\nargs = [\"mcp\"]\ndefault_tools_approval_mode = \"prompt\"\n\n[mcp_servers.t4.env]\nA = \"1\"\n\n[other]\nx = 1\n"
	want := "[mcp_servers.t4]\n# mine\ncommand = \"/opt/t4\"\nstartup_timeout_sec = 30\nargs = [\"mcp\", \"--agent\", \"codex\"]\ndefault_tools_approval_mode = \"prompt\"\n\n[mcp_servers.t4.env]\nA = \"1\"\n\n[other]\nx = 1\n"
	if got, changed := edit(t, stale); !changed || got != want {
		t.Errorf("changed=%v:\n%s\nwant:\n%s", changed, got, want)
	}

	// A table with only some of the keys gets the others, before the next table.
	partial := "[mcp_servers.t4]\ncommand = \"/opt/t4\"\n\n[mcp_servers.t4.env]\nA = \"1\"\n"
	want = "[mcp_servers.t4]\ncommand = \"/opt/t4\"\nargs = [\"mcp\", \"--agent\", \"codex\"]\ndefault_tools_approval_mode = \"approve\"\n\n[mcp_servers.t4.env]\nA = \"1\"\n"
	if got, _ := edit(t, partial); got != want {
		t.Errorf("partial table:\n%s\nwant:\n%s", got, want)
	}
}

func TestCodexEditKeepsCRLF(t *testing.T) {
	out, _ := edit(t, "model = \"x\"\r\n")
	if strings.Count(out, "\n") != strings.Count(out, "\r\n") || !strings.Contains(out, "[mcp_servers.t4]\r\n") {
		t.Errorf("mixed line endings: %q", out)
	}
	again, changed := edit(t, out)
	if changed || again != out {
		t.Errorf("a second edit changed the CRLF file: %q", again)
	}
	crStale := strings.ReplaceAll("[mcp_servers.t4]\ncommand = \"/old\"\nargs = [\"mcp\"]\n", "\n", "\r\n")
	got, _ := edit(t, crStale)
	if strings.Count(got, "\n") != strings.Count(got, "\r\n") || !strings.Contains(got, "command = \"/opt/t4\"\r\n") {
		t.Errorf("stale CRLF table: %q", got)
	}
}

// A table header inside a multi-line string is text, not a table.
func TestCodexEditIgnoresHeadersInStrings(t *testing.T) {
	before := "notes = \"\"\"\n[mcp_servers.t4]\ncommand = \"fake\"\n\"\"\"\n"
	out, _ := edit(t, before)
	if want := before + "\n" + tomlTable; out != want {
		t.Errorf("got:\n%s", out)
	}
}

// Forms join doesn't edit are refused, rather than guessed at: a second definition of the table
// would make the file invalid, and Codex then loads none of its config.
func TestCodexEditRefusesFormsItDoesNotEdit(t *testing.T) {
	for name, text := range map[string]string{
		"dotted key":     "mcp_servers.t4.command = \"x\"\n",
		"inline table":   "mcp_servers = { t4 = { command = \"x\" } }\n",
		"multiline args": "[mcp_servers.t4]\ncommand = \"/opt/t4\"\nargs = [\n  \"mcp\",\n]\n",
	} {
		if out, _, err := editCodexConfig(text, "/opt/t4"); err == nil {
			t.Errorf("%s: edited it:\n%s", name, out)
		}
	}
	// The same key names inside another table are that table's own.
	if _, _, err := editCodexConfig("[profiles.p]\nmcp_servers = 1\n", "/opt/t4"); err != nil {
		t.Errorf("refused a key that belongs to another table: %v", err)
	}
}

// A header may quote its keys and carry a comment; neither changes where a table starts or ends
// (Codex review, 2026-10-29: the comment kept the header from parsing, and the table before it
// swallowed the other server's lines).
func TestCodexEditReadsQuotedHeadersWithComments(t *testing.T) {
	before := tomlTable + "\n[mcp_servers.\"other\"] # personal\ncommand = \"o\"\nargs = [\"x\"]\n"
	if out, changed := edit(t, before); changed {
		t.Errorf("changed a table that was right:\n%s", out)
	}
	// The same spelling for t4's own table is that table, edited in place, not a second one.
	own := "[ mcp_servers . \"t4\" ] # mine\ncommand = '/old/t4' # keep\n\n[other]\nx = 1\n"
	got, changed := edit(t, own)
	want := "[ mcp_servers . \"t4\" ] # mine\ncommand = \"/opt/t4\"\nargs = [\"mcp\", \"--agent\", \"codex\"]\ndefault_tools_approval_mode = \"approve\"\n\n[other]\nx = 1\n"
	if !changed || got != want {
		t.Errorf("changed=%v:\n%s\nwant:\n%s", changed, got, want)
	}
	// A comment after a value that is already right isn't a difference.
	commented := "[mcp_servers.t4]\ncommand = \"/opt/t4\" # mine\nargs = [\"mcp\", \"--agent\", \"codex\"] # mine\ndefault_tools_approval_mode = \"approve\"\n"
	if out, changed := edit(t, commented); changed {
		t.Errorf("rewrote a value that was right:\n%s", out)
	}
}

// t4's server can also be defined inside [mcp_servers] itself; a table of join's own would then be
// a second definition, and the file would no longer parse.
func TestCodexEditRefusesT4InsideTheParentTable(t *testing.T) {
	for name, text := range map[string]string{
		"inline table": "[mcp_servers]\nt4 = { command = \"t4\", args = [\"mcp\"] }\n",
		"dotted key":   "[mcp_servers]\nt4.command = \"t4\"\n",
		"quoted key":   "[mcp_servers]\n\"t4\".command = 'x'\n",
	} {
		if out, _, err := editCodexConfig(text, "/opt/t4"); err == nil {
			t.Errorf("%s: edited it:\n%s", name, out)
		}
	}
	// Other servers in the parent table are theirs, and t4's table goes after them.
	before := "[mcp_servers]\nother = { command = \"o\" }\n"
	if out, changed := edit(t, before); !changed || out != before+"\n"+tomlTable {
		t.Errorf("changed=%v:\n%s", changed, out)
	}
}

func TestCodexEditQuotesWindowsPaths(t *testing.T) {
	out, _, err := editCodexConfig("", `C:\Users\a b\bin\t4.exe`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `command = "C:\\Users\\a b\\bin\\t4.exe"`) {
		t.Errorf("the path isn't written as a TOML string:\n%s", out)
	}
	if _, changed, _ := editCodexConfig(out, `C:\Users\a b\bin\t4.exe`); changed {
		t.Error("a second edit changed it")
	}
}
