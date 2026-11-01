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
//     counts: join keeps {"action": "murmur_*", "resource": "*", "effect": "allow"} last in the
//     top-level "permissions", so a broad rule of the user's doesn't make each call ask.
//   - With both opencode.json and opencode.jsonc there, OpenCode reads the .jsonc last, and its
//     entries win (Codex's review of #136, 2026-11-01): join writes there.
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

// openCodeFile is the config file in dir join edits: opencode.jsonc if it is there, which OpenCode
// reads after opencode.json, so that what it says wins; else opencode.json, new if need be. other
// is opencode.json when both are there, "" otherwise.
func openCodeFile(dir string) (path, other string) {
	json, jsonc := filepath.Join(dir, "opencode.json"), filepath.Join(dir, "opencode.jsonc")
	switch {
	case exists(jsonc) && exists(json):
		return jsonc, json
	case exists(jsonc):
		return jsonc, ""
	}
	return json, ""
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
	path, other := openCodeFile(h.Dir)
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
	// OpenCode merges the two files, so an entry for the server in the one read first would add
	// its keys to the one join writes (a "disabled": true, say): it is made the same.
	var otherDid []string
	if other != "" {
		ob, err := os.ReadFile(other)
		if err != nil {
			return nil, false, err
		}
		if _, found, err := jsonedit.GetIn(string(ob), []string{"mcp", "servers", brand.MCPServer}); err != nil {
			return nil, false, fmt.Errorf("%s: %w", other, err)
		} else if found {
			oout, ochanged, err := jsonedit.SetIn(string(ob), []string{"mcp", "servers", brand.MCPServer}, entry)
			if err != nil {
				return nil, false, fmt.Errorf("%s: %w", other, err)
			}
			if ochanged {
				if err := writeAtomic(other, []byte(oout), 0o644); err != nil {
					return nil, false, fmt.Errorf("%s: %w", other, err)
				}
				otherDid = append(otherDid, h.Label+": made the "+brand.MCPServer+" server in "+other+" the same as in "+path)
			}
		}
	}
	if !changed && !allowed {
		return otherDid, len(otherDid) > 0, nil
	}
	if err := writeAtomic(path, []byte(out), 0o644); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	did = otherDid
	if changed {
		did = append(did, h.Label+": registered the "+brand.MCPServer+" MCP server for you, in every project ("+path+")")
	}
	if allowed {
		did = append(did, h.Label+": allowed "+brand.Product+"'s tools, so they don't ask each time ("+path+", permissions)")
	}
	return did, changed || len(otherDid) > 0, nil
}

func openCodeRemoval(_ Env, h agentHome) string {
	path, _ := openCodeFile(h.Dir)
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
