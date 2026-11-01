package join

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/jsonedit"
)

// OpenCode (2.x) keeps its user config in opencode.json or opencode.jsonc in its config directory,
// ~/.config/opencode, or $XDG_CONFIG_HOME/opencode. Either may be JSONC, with comments and trailing
// commas, and people edit it by hand, so join edits it as text (jsonedit.SetIn): one member added
// or replaced, every other byte left. Seen on OpenCode 2.2.0 and in its documentation
// (opencode.ai/v2/docs), 2026-11-01:
//
//   - A server is mcp.servers.<name>, {"type": "local", "command": [program, args...]}; the older
//     mcp.<name> is deprecated. "codemode": false puts the tools on the agent's own list, as
//     murmur_who and so on; by default OpenCode hides an MCP server's tools behind one `execute`
//     tool of its own, and the agent has to know to look.
//   - OpenCode starts one server process per server and directory, which every session there
//     shares, and names the session in each call's _meta (see internal/mcpserver, sharedAgent). The
//     process gets OpenCode's environment, T3CODE_CLI_PATH included when T3 started OpenCode.
//   - Its permissions allow everything unless a rule says otherwise, and the last matching rule
//     counts: join appends {"action": "murmur_*", "resource": "*", "effect": "allow"} to the
//     top-level "permissions", so a broad rule of the user's doesn't make each call ask.
//   - It watches its config: a server added while it runs connects within seconds, and a session
//     already open has its tools from its next turn.

var openCodeArgs = []string{"mcp", "--agent", "opencode"}

func openCodeAllowRule() *jsonedit.Object {
	r := jsonedit.New()
	r.Set("action", brand.MCPServer+"_*")
	r.Set("resource", "*")
	r.Set("effect", "allow")
	return r
}

// openCodeFile is the config file in dir join edits: opencode.json if it is there, else
// opencode.jsonc if that is, else a new opencode.json.
func openCodeFile(dir string) string {
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		if p := filepath.Join(dir, name); exists(p) {
			return p
		}
	}
	return filepath.Join(dir, "opencode.json")
}

// registerOpenCodeAt puts Murmur's server in OpenCode's config in h and allows its tools. OpenCode
// isn't touched where neither its command nor the directory is there.
func (e Env) registerOpenCodeAt(h agentHome) (did []string, fresh bool, err error) {
	if h.Dir == "" {
		return nil, false, nil
	}
	if _, lerr := e.look("opencode"); lerr != nil && h.Bin == "" && !isDir(h.Dir) {
		return nil, false, nil
	}
	path := openCodeFile(h.Dir)
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, false, err
	}
	entry := jsonedit.New()
	entry.Set("type", "local")
	entry.Set("command", append([]any{e.Exe}, toAny(openCodeArgs)...))
	entry.Set("codemode", false)
	out, changed, err := jsonedit.SetIn(string(b), []string{"mcp", "servers", brand.MCPServer}, entry)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w, so the server wasn't added", path, err)
	}
	out, allowed, err := jsonedit.AppendIn(out, []string{"permissions"}, openCodeAllowRule())
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w, so the server wasn't added", path, err)
	}
	if !changed && !allowed {
		return nil, false, nil
	}
	if err := writeAtomic(path, []byte(out), 0o644); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	if changed {
		did = append(did, h.Label+": registered the "+brand.MCPServer+" MCP server for you, in every project ("+path+")")
	}
	if allowed {
		did = append(did, h.Label+": allowed "+brand.Product+"'s tools, so they don't ask each time ("+path+", permissions)")
	}
	return did, changed, nil
}

func openCodeRemoval(_ Env, h agentHome) string {
	path := openCodeFile(h.Dir)
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if _, found, _ := jsonedit.GetIn(string(b), []string{"mcp", "servers", brand.MCPServer}); !found {
		return ""
	}
	return "delete \"" + brand.MCPServer + "\" from mcp.servers, and the " + brand.MCPServer + "_* rule from permissions, in " + path
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
