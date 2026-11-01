package join

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/jsonedit"
)

// Pi (the pi coding agent, 0.99) has MCP built in: servers in mcp.json in its agent directory
// (~/.pi/agent, or $PI_CODING_AGENT_DIR), one server process per session, with Pi's environment.
// But nothing in that process says which session it is: Pi gives the session's ID and file only to
// its own shell tools, and T3 Code knows a Pi thread by the session's file. So join registers
// Murmur's server through a Pi extension instead, a file of Murmur's own in the agent directory's
// extensions/, which Pi loads in every session: it registers the server as Pi loads it, which makes
// it a server Pi waits for before the first prompt, and again at each session's start with the
// session's file in MURMUR_PI_SESSION_FILE (pi.registerMcpServer, docs/extensions.md and
// docs/mcp.md of Pi's package, read 2026-11-01; Pi 0.99.0 showed it connected with its five tools,
// 2026-11-01). "exposure": "direct" lists the tools themselves; Pi's
// default hides them behind a tool of its own. Pi asks before no tool call; in T3, T3's own Pi
// extension asks before each one outside full-access mode. A server named murmur in mcp.json would
// win over the extension's, and its sessions would all look like one.

// piExtension is the extension's text for the mur at exe.
func piExtension(exe string) string {
	cmd, _ := json.Marshal(exe)
	return `// Written by ` + "`" + brand.Command + ` join` + "`" + ` (` + brand.Product + `, https://github.com/a-Fig/murmur). ` + brand.Command + ` join writes it again when ` + brand.Command + ` moves.
// It registers ` + brand.Product + `'s MCP server for each Pi session, with the session's file in its
// environment: that is how ` + brand.Product + ` tells Pi sessions apart, and finds a session's T3 Code thread.
// Delete this file to take ` + brand.Product + ` out of Pi.
export default function (pi: any) {
  const register = (file: string) =>
    pi.registerMcpServer("` + brand.MCPServer + `", {
      command: ` + string(cmd) + `,
      args: ["mcp", "--agent", "pi"],
      exposure: "direct",
      env: { MURMUR_PI_SESSION_FILE: file },
    });
  // Registered as Pi loads, the server is one Pi waits for before the first prompt; registered
  // again when the session starts, it has the session's file.
  register("");
  pi.on("session_start", (_event: any, ctx: any) => {
    register(String(ctx?.sessionManager?.getSessionFile?.() ?? ""));
  });
}
`
}

func piExtensionPath(dir string) string {
	return filepath.Join(dir, "extensions", brand.MCPServer+".ts")
}

// registerPiAt writes Murmur's extension into Pi's agent directory h. Pi isn't touched where neither
// its command nor the directory is there.
func (e Env) registerPiAt(h agentHome) (did []string, fresh bool, err error) {
	if h.Dir == "" {
		return nil, false, nil
	}
	if _, lerr := e.look("pi"); lerr != nil && h.Bin == "" && !isDir(h.Dir) {
		return nil, false, nil
	}
	path := piExtensionPath(h.Dir)
	want := piExtension(e.Exe)
	b, err := os.ReadFile(path)
	switch {
	case err == nil && strings.ReplaceAll(string(b), "\r\n", "\n") == want:
		return nil, false, piMCPConflict(h.Dir)
	case err == nil && !strings.HasPrefix(string(b), "// Written by `"+brand.Command+" join`"):
		return nil, false, fmt.Errorf("%s is there and isn't the one join writes, so it was left alone", path)
	case err != nil && !os.IsNotExist(err):
		return nil, false, err
	}
	if err := writeAtomic(path, []byte(want), 0o644); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	return []string{h.Label + ": registered the " + brand.MCPServer + " MCP server for you, in every project, through an extension (" + path + ")"}, true, piMCPConflict(h.Dir)
}

// piMCPConflict is an error if Pi's mcp.json in dir has a server named murmur, which Pi runs instead
// of the extension's: with whatever command it names, and without the session's file (Codex's
// review of #136). join leaves the user's file alone and says what to delete.
func piMCPConflict(dir string) error {
	path := filepath.Join(dir, "mcp.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if _, found, _ := jsonedit.GetIn(string(b), []string{"mcpServers", brand.MCPServer}); found {
		return fmt.Errorf("%s has a server named %s, which Pi runs instead of %s's extension: delete it from there", path, brand.MCPServer, brand.Product)
	}
	return nil
}

func piRemoval(_ Env, h agentHome) string {
	if path := piExtensionPath(h.Dir); exists(path) {
		return "delete " + path
	}
	return ""
}
