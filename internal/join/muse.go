package join

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/jsonedit"
)

// Muse Code (Meta's agent) keeps its user settings in settings.json in $XDG_CONFIG_HOME/muse, else
// ~/.config/muse (%USERPROFILE%\.config\muse on Windows). Seen on Muse 1.4.14, 2026-11-01:
//
//   - The file is strict JSON: a comment in it, and Muse refuses to start. join edits it as text
//     (jsonedit.SetIn), writing JSON only.
//   - A server is mcpServers.<name>: command, args, env (strings), and mode. A server is "required"
//     unless its mode says "optional", and Muse won't start a session whose required server fails,
//     so Murmur's is optional. A file that has the older mcp_servers too, or a server with both
//     required and mode, loses every MCP server: join refuses a file with mcp_servers.
//   - Muse starts one server process per session and gives it MUSE_SESSION_ID, the session's ID,
//     which T3 Code also keeps for the thread. Of its own environment it passes on only a few
//     variables (PATH, TEMP, HOME, SYSTEMROOT and the like) and what env names; a ${VAR:-} there is
//     Muse's variable, empty if unset (a ${VAR} with no default that is unset makes Muse skip the
//     server, silently). So env forwards T3CODE_CLI_PATH and T3CODE_HOME, which say the session is
//     in T3, and, on Windows, the variables Go and git find the user's home and credentials by.
//   - There is no setting found that allows a server's tools without asking: in T3, outside
//     full-access mode, each Murmur call asks once until "Always allow" is chosen.
//   - Servers are read when a session starts; a session already open doesn't get one added later.

var museArgs = []string{"mcp", "--agent", "muse"}

// museEnv are the variables Murmur's server needs from Muse's environment.
func museEnv() []string {
	vars := []string{t3CLIEnv, "T3CODE_HOME"}
	if runtime.GOOS == "windows" {
		vars = append(vars, "USERPROFILE", "APPDATA", "LOCALAPPDATA", "HOMEDRIVE", "HOMEPATH", "USERNAME", "PROGRAMDATA")
	}
	return vars
}

// registerMuseAt puts Murmur's server in Muse's settings.json in h. Muse isn't touched where neither
// its command nor the directory is there.
func (e Env) registerMuseAt(h agentHome) (did []string, fresh bool, err error) {
	if h.Dir == "" {
		return nil, false, nil
	}
	if _, lerr := e.look("muse"); lerr != nil && h.Bin == "" && !isDir(h.Dir) {
		return nil, false, nil
	}
	path := filepath.Join(h.Dir, "settings.json")
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, false, err
	}
	text := string(b)
	if _, legacy, err := jsonedit.GetIn(text, []string{"mcp_servers"}); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	} else if legacy {
		return nil, false, fmt.Errorf("%s: %w", path, errors.New("it has the older mcp_servers key, and Muse drops every server when it finds that and mcpServers both: move its servers to mcpServers, and run join again"))
	}
	if len(b) == 0 {
		text = "{\n  \"schema_version\": 1\n}\n"
	}
	env := jsonedit.New()
	for _, v := range museEnv() {
		env.Set(v, "${"+v+":-}")
	}
	entry := jsonedit.New()
	entry.Set("command", e.Exe)
	entry.Set("args", toAny(museArgs))
	entry.Set("env", env)
	entry.Set("mode", "optional")
	out, changed, err := jsonedit.SetIn(text, []string{"mcpServers", brand.MCPServer}, entry)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w, so the server wasn't added", path, err)
	}
	if !changed {
		return nil, false, nil
	}
	if err := writeAtomic(path, []byte(out), 0o644); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	return []string{h.Label + ": registered the " + brand.MCPServer + " MCP server for you, in every project (" + path + ")"}, true, nil
}

func museRemoval(_ Env, h agentHome) string {
	path := filepath.Join(h.Dir, "settings.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if _, found, _ := jsonedit.GetIn(string(b), []string{"mcpServers", brand.MCPServer}); !found {
		return ""
	}
	return "delete \"" + brand.MCPServer + "\" from mcpServers in " + path
}
