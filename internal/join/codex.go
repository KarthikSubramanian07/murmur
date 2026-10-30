package join

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/a-Fig/t4/internal/brand"
)

// Codex keeps its MCP servers in config.toml, a file the user also edits by hand and Codex rewrites
// whole when asked to add a server (`codex mcp add` reorders keys and drops empty ones, seen on
// 0.164.0, 2026-10-29). So join edits it as text: it adds one table at the end, or changes the
// lines of its own table in place, and every other byte stays.

var codexArgs = []string{"mcp", "--agent", "codex"}

// editCodexConfig returns config.toml's text with t4's server in it. changed is false, and out is
// text, when the table already says what join would write. It refuses (an error) a file whose t4
// entry is written in a form it doesn't edit: two definitions of one table make the file invalid,
// and Codex then fails to load any of its config.
func editCodexConfig(text, exe string) (out string, changed bool, err error) {
	nl := "\n"
	if strings.Contains(text, "\r\n") {
		nl = "\r\n"
	}
	lines := strings.Split(text, "\n") // a CRLF file keeps its \r on each line, which isn't touched
	start, end := -1, len(lines)
	inString := ""
	var table []string // the table the line is in; none before the first header
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if inString != "" {
			if strings.Count(line, inString)%2 == 1 {
				inString = ""
			}
			continue
		}
		for _, q := range []string{`"""`, `'''`} {
			if strings.Count(line, q)%2 == 1 {
				inString = q
			}
		}
		if path, ok := tableHeader(line); ok {
			table = path
			switch {
			case start >= 0 && end == len(lines):
				end = i
			case start < 0 && len(path) == 2 && path[0] == "mcp_servers" && path[1] == brand.MCPServer && !strings.HasPrefix(strings.TrimSpace(line), "[["):
				start = i
			}
			continue
		}
		// A key that defines the server, or all the servers, without a [mcp_servers.t4] header of its
		// own: `mcp_servers.x.command = ...` or `mcp_servers = {...}` before the first header, and
		// `t4 = {...}` or `t4.command = ...` under [mcp_servers]. Join's own table would define it twice.
		if key, _, ok := keyValue(line); ok {
			if (len(table) == 0 && key[0] == "mcp_servers") || (len(table) == 1 && table[0] == "mcp_servers" && key[0] == brand.MCPServer) {
				return "", false, errors.New("config.toml sets the mcp_servers table, or the " + brand.MCPServer + " server in it, in a form join doesn't edit (a dotted key or an inline table)")
			}
		}
	}

	want := map[string]string{
		"command": "command = " + tomlString(exe),
		"args":    "args = [" + joinQuoted(codexArgs) + "]",
	}
	approve := `default_tools_approval_mode = "approve"`

	if start < 0 {
		var b strings.Builder
		b.WriteString(text)
		if text != "" && !strings.HasSuffix(text, "\n") {
			b.WriteString(nl)
		}
		if strings.TrimSpace(text) != "" {
			b.WriteString(nl)
		}
		for _, l := range []string{"[mcp_servers." + brand.MCPServer + "]", want["command"], want["args"], approve} {
			b.WriteString(l + nl)
		}
		return b.String(), true, nil
	}

	block := append([]string(nil), lines[start+1:end]...)
	found := map[string]bool{}
	for i, raw := range block {
		line := strings.TrimRight(raw, "\r")
		path, val, ok := keyValue(line)
		if !ok || len(path) != 1 {
			continue
		}
		key := path[0]
		cr := raw[len(line):]
		switch key {
		case "command", "args":
			found[key] = true
			if !singleLine(val) {
				return "", false, fmt.Errorf("config.toml has %s of the %s server on several lines, which join doesn't edit", key, brand.MCPServer)
			}
			if !sameValue(key, val, exe) {
				block[i] = want[key] + cr
			}
		case "default_tools_approval_mode":
			found[key] = true // the user's own setting, kept
		}
	}
	// Keys the table lacks go after its last line that has anything on it.
	at := len(block)
	for at > 0 && strings.TrimSpace(block[at-1]) == "" {
		at--
	}
	var add []string
	for _, key := range []string{"command", "args"} {
		if !found[key] {
			add = append(add, want[key])
		}
	}
	if !found["default_tools_approval_mode"] {
		add = append(add, approve)
	}
	for i := range add {
		if nl == "\r\n" {
			add[i] += "\r"
		}
	}
	block = append(block[:at], append(add, block[at:]...)...)

	rebuilt := append(append(append([]string(nil), lines[:start+1]...), block...), lines[end:]...)
	out = strings.Join(rebuilt, "\n")
	return out, out != text, nil
}

// tableHeader parses a line that is a table header, [a.b] or [[a.b]], into its key path. Only a
// comment may follow the header.
func tableHeader(line string) ([]string, bool) {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "[") {
		return nil, false
	}
	closing := "]"
	if strings.HasPrefix(t, "[[") {
		closing = "]]" // array of tables
	}
	path, rest, ok := keyPath(strings.TrimLeft(t, "[")) // past the opening brackets of either form
	if !ok || !strings.HasPrefix(rest, closing) {
		return nil, false
	}
	rest = strings.TrimSpace(strings.TrimPrefix(rest, closing))
	return path, rest == "" || rest[0] == '#'
}

// keyValue splits a `key = value` line into its key path (a.b.c is three parts) and its value.
func keyValue(line string) (path []string, val string, ok bool) {
	path, rest, ok := keyPath(strings.TrimSpace(line))
	if !ok || !strings.HasPrefix(rest, "=") {
		return nil, "", false
	}
	return path, strings.TrimSpace(rest[1:]), true
}

// keyPath reads a dotted key (bare or quoted parts, spaces allowed around the dots) from the start of
// t and returns it with what follows, left-trimmed.
func keyPath(t string) (path []string, rest string, ok bool) {
	for {
		t = strings.TrimLeft(t, " \t")
		var part string
		switch {
		case t == "":
			return nil, "", false
		case t[0] == '"' || t[0] == '\'':
			end := closingQuote(t)
			if end < 0 {
				return nil, "", false
			}
			if part, ok = tomlUnquote(t[:end+1]); !ok {
				return nil, "", false
			}
			t = t[end+1:]
		default:
			end := 0
			for end < len(t) && (t[end] == '-' || t[end] == '_' || t[end] >= '0' && t[end] <= '9' || t[end] >= 'a' && t[end] <= 'z' || t[end] >= 'A' && t[end] <= 'Z') {
				end++
			}
			if end == 0 {
				return nil, "", false
			}
			part, t = t[:end], t[end:]
		}
		path = append(path, part)
		t = strings.TrimLeft(t, " \t")
		if !strings.HasPrefix(t, ".") {
			return path, t, true
		}
		t = t[1:]
	}
}

// closingQuote is the index of the quote that ends the string t starts with, or -1.
func closingQuote(t string) int {
	if t[0] == '\'' {
		if end := strings.IndexByte(t[1:], '\''); end >= 0 {
			return end + 1
		}
		return -1
	}
	for j := 1; j < len(t); j++ {
		switch t[j] {
		case '\\':
			j++
		case '"':
			return j
		}
	}
	return -1
}

// singleLine reports whether a value is complete on its line.
func singleLine(val string) bool {
	switch {
	case strings.HasPrefix(val, `"""`), strings.HasPrefix(val, `'''`):
		return false
	case strings.HasPrefix(val, "["):
		return strings.Count(val, "[") == strings.Count(val, "]")
	}
	return true
}

// sameValue reports whether a table's command or args value already says what join writes. A
// value it can't read counts as different, and is replaced.
func sameValue(key, val, exe string) bool {
	items, ok := stringList(val)
	if key == "command" {
		s, ok := tomlUnquote(stripComment(val))
		return ok && s == exe
	}
	if !ok || len(items) != len(codexArgs) {
		return false
	}
	for i := range items {
		if items[i] != codexArgs[i] {
			return false
		}
	}
	return true
}

func stripComment(val string) string {
	if len(val) > 0 && (val[0] == '"' || val[0] == '\'') {
		if end := closingQuote(val); end >= 0 {
			return val[:end+1]
		}
		return val
	}
	if i := strings.Index(val, "#"); i >= 0 {
		return strings.TrimSpace(val[:i])
	}
	return val
}

// stringList reads ["a", 'b'] on one line.
func stringList(val string) ([]string, bool) {
	val = strings.TrimSpace(val)
	if !strings.HasPrefix(val, "[") {
		return nil, false
	}
	rest := strings.TrimSpace(val[1:])
	var out []string
	for {
		if strings.HasPrefix(rest, "]") {
			return out, true
		}
		if rest == "" || (rest[0] != '"' && rest[0] != '\'') {
			return nil, false
		}
		var s string
		var n int
		if rest[0] == '\'' {
			end := strings.IndexByte(rest[1:], '\'')
			if end < 0 {
				return nil, false
			}
			s, n = rest[1:1+end], end+2
		} else {
			end := -1
			for j := 1; j < len(rest); j++ {
				if rest[j] == '\\' {
					j++
				} else if rest[j] == '"' {
					end = j
					break
				}
			}
			if end < 0 {
				return nil, false
			}
			var ok bool
			if s, ok = tomlUnquote(rest[:end+1]); !ok {
				return nil, false
			}
			n = end + 1
		}
		out = append(out, s)
		rest = strings.TrimSpace(rest[n:])
		rest = strings.TrimSpace(strings.TrimPrefix(rest, ","))
	}
}

// tomlString writes s as a TOML basic string.
func tomlString(s string) string { return strconv.Quote(s) }

// tomlUnquote reads a single-line TOML string, basic or literal.
func tomlUnquote(v string) (string, bool) {
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1], true
	}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		s, err := strconv.Unquote(v)
		return s, err == nil
	}
	return "", false
}

func joinQuoted(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = tomlString(s)
	}
	return strings.Join(q, ", ")
}
