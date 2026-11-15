package hook

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/a-Fig/murmur/internal/proto"
)

// The GitHub issues a tool call filed or closed (#510), so that the page's Recent view can name the
// session that did it: GitHub says only which login opened an issue, and the agents on one machine
// share their user's. The hook reads them from the call's input and output, which an agent's hook
// is given after the call: Claude Code's PostToolUse as tool_input and tool_response, Codex's under
// the same names (codex-rs hooks, rust-v0.169.0), Grok's as toolInput, with tool_response beside its
// own toolResult (Grok Build's docs/user-guide/10-hooks.md, read 2026-11-15). Pi's extension and
// OpenCode's plugin pass neither, so an issue their sessions file shows under the login.
//
// Only calls of a known shape are read: a shell command that runs `gh issue create` or `gh issue
// close`, and an MCP tool of GitHub's server that creates an issue or closes one. Any other call's
// output is never looked at, and of these only the issue's repository and number go to the daemon.

// shellTools are the agents' names for the tool that runs a command: Claude Code's and Codex's Bash,
// and Grok's run_terminal_command.
var shellTools = map[string]bool{"Bash": true, "run_terminal_command": true}

// maxIssues bounds what one call can report.
const maxIssues = 10

var (
	ghIssueCmd = regexp.MustCompile(`\bgh\s+issue\s+(create|new|close)\b`)
	// What gh prints (cli/cli's pkg/cmd/issue, read 2026-11-15): `create` the new issue's address
	// on a line of its own, and `close` "✓ Closed issue owner/name#37 (title)", on stderr whether
	// or not that is a terminal. An issue that was closed already is "! Issue ... is already closed".
	createdLine = regexp.MustCompile(`(?m)^\s*https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/issues/([0-9]+)\s*$`)
	closedLine  = regexp.MustCompile(`Closed issue ([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)#([0-9]+)\b`)
	issueURL    = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/issues/([0-9]+)$`)
)

// issuesOf is the issues the call in's payload describes filed or closed, if any.
func issuesOf(in input) []proto.Issue {
	if shellTools[in.ToolName] {
		return shellIssues(in.ToolInput, in.ToolResponse)
	}
	if i := strings.LastIndex(in.ToolName, "__"); i > 0 {
		return mcpIssues(in.ToolName[i+2:], in.ToolInput, in.ToolResponse)
	}
	return nil
}

// shellIssues reads what gh printed, for a command that ran it to file or close an issue.
func shellIssues(input, response json.RawMessage) []proto.Issue {
	var args struct {
		Command any `json:"command"` // a string; a list of words where an agent passes argv
	}
	if json.Unmarshal(input, &args) != nil {
		return nil
	}
	command := ""
	switch c := args.Command.(type) {
	case string:
		command = c
	case []any:
		for _, w := range c {
			if s, ok := w.(string); ok {
				command += s + " "
			}
		}
	}
	var created, closed bool
	for _, m := range ghIssueCmd.FindAllStringSubmatch(command, -1) {
		if m[1] == "close" {
			closed = true
		} else {
			created = true
		}
	}
	if !created && !closed {
		return nil
	}
	var v any
	if json.Unmarshal(response, &v) != nil {
		return nil
	}
	var out []proto.Issue
	add := func(m []string, isClosed bool) {
		n, err := strconv.Atoi(m[3])
		if err != nil || n <= 0 || len(out) >= maxIssues {
			return
		}
		is := proto.Issue{Repo: m[1] + "/" + m[2], Number: n, Closed: isClosed}
		for _, o := range out {
			if o == is {
				return
			}
		}
		out = append(out, is)
	}
	walk(v, func(s string) {
		if created {
			for _, m := range createdLine.FindAllStringSubmatch(s, maxIssues) {
				add(m, false)
			}
		}
		if closed {
			for _, m := range closedLine.FindAllStringSubmatch(s, maxIssues) {
				add(m, true)
			}
		}
	})
	return out
}

// mcpIssues reads the issue a GitHub MCP tool filed or closed, named tool without its server's
// prefix: create_issue and update_issue, and issue_write with a method, of github/github-mcp-server.
// The issue is the one whose address the result gives; a call whose result names none, or says it
// failed, reports nothing.
func mcpIssues(tool string, input, response json.RawMessage) []proto.Issue {
	var args struct {
		Method string `json:"method"`
		State  string `json:"state"`
		Number any    `json:"issue_number"`
	}
	if json.Unmarshal(input, &args) != nil {
		return nil
	}
	closed := false
	switch {
	case tool == "create_issue", tool == "issue_write" && args.Method == "create":
	case tool == "update_issue", tool == "issue_write" && args.Method == "update":
		if args.State != "closed" {
			return nil
		}
		closed = true
	default:
		return nil
	}
	var v any
	if json.Unmarshal(response, &v) != nil {
		return nil
	}
	m := resultIssue(v)
	if m == nil {
		return nil
	}
	n, err := strconv.Atoi(m[3])
	if err != nil || n <= 0 {
		return nil
	}
	if closed && args.Number != nil && fmtNumber(args.Number) != m[3] {
		return nil // the result names another issue than the one asked to be closed
	}
	return []proto.Issue{{Repo: m[1] + "/" + m[2], Number: n, Closed: closed}}
}

// resultIssue is the address of the issue an MCP tool's result describes, split by issueURL: the
// html_url or url of the shallowest object that has one. A result's text that is JSON is read as
// such. Nil if there is none, or the result is an error.
func resultIssue(v any) []string {
	queue := []any{v}
	for seen := 0; len(queue) > 0 && seen < 10000; seen++ {
		x := queue[0]
		queue = queue[1:]
		switch t := x.(type) {
		case map[string]any:
			if e, _ := t["isError"].(bool); e {
				return nil
			}
			for _, k := range []string{"html_url", "url"} {
				if s, ok := t[k].(string); ok {
					if m := issueURL.FindStringSubmatch(s); m != nil {
						return m
					}
				}
			}
			for _, y := range t {
				queue = append(queue, y)
			}
		case []any:
			queue = append(queue, t...)
		case string:
			if s := strings.TrimSpace(t); strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
				var y any
				if json.Unmarshal([]byte(s), &y) == nil {
					queue = append(queue, y)
				}
			}
		}
	}
	return nil
}

// walk calls f on every string in v, a decoded JSON value, up to a bound.
func walk(v any, f func(string)) {
	stack := []any{v}
	for seen := 0; len(stack) > 0 && seen < 10000; seen++ {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch t := x.(type) {
		case string:
			f(t)
		case []any:
			stack = append(stack, t...)
		case map[string]any:
			for _, y := range t {
				stack = append(stack, y)
			}
		}
	}
}

// fmtNumber is a JSON number or string as the digits it holds.
func fmtNumber(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case string:
		return strings.TrimSpace(n)
	}
	return ""
}
