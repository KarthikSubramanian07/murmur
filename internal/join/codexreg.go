package join

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/a-Fig/murmur/internal/brand"
)

// registerCodex puts Murmur's MCP server in Codex's config.toml, for this user in every project.
// fresh reports whether the file changed. Codex isn't touched on a machine that has neither its
// command nor its home directory.
func (e Env) registerCodex() (did []string, fresh bool, err error) {
	if _, lerr := e.look("codex"); lerr != nil && !isDir(e.CodexHome) {
		return nil, false, nil
	}
	path := filepath.Join(e.CodexHome, "config.toml")
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, false, err
	}
	out, changed, err := editCodexConfig(string(b), e.Exe)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	if !changed {
		return nil, false, nil
	}
	if err := writeAtomic(path, []byte(out), 0o600); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	return []string{"Codex: registered the " + brand.MCPServer + " MCP server for you, in every project, and allowed its tools (" + path + ")"}, true, nil
}
