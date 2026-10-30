package join

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/a-Fig/t4/internal/brand"
	"github.com/a-Fig/t4/internal/jsonedit"
)

// allowRule is the permission that lets an agent call every tool of t4's server without asking.
// `mcp__<server>` with nothing after it is Claude Code's own spelling for that (seen on 2.1.312,
// 2026-10-29).
var allowRule = "mcp__" + brand.MCPServer

// registerClaude makes Claude Code know t4's server, for this user in every project, and lets its
// tools run without a prompt each time. fresh reports whether the server was new to Claude Code (or
// moved), which is when sessions that are already running have never heard of it.
func (e Env) registerClaude(ctx context.Context) (did []string, fresh bool, err error) {
	claude, err := e.look("claude")
	haveCLI := err == nil
	if !haveCLI && !exists(e.ClaudeJSON) && !exists(e.ClaudeDir) {
		return nil, false, nil // no Claude Code here
	}

	cfg, raw, err := jsonedit.ReadFile(e.ClaudeJSON)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", e.ClaudeJSON, err)
	}
	servers := cfg.Object("mcpServers")
	entry := servers.Object(brand.MCPServer) // nil-safe: a nil *Object has no keys
	if !ownsEntry(entry, e.Exe) {
		fresh = true
		switch {
		case haveCLI:
			if entry != nil {
				// A stale entry (the binary moved): `add` refuses a name that exists.
				e.runClaude(ctx, claude, "mcp", "remove", "--scope", "user", brand.MCPServer)
			}
			if out, err := e.runClaude(ctx, claude, "mcp", "add", "--scope", "user", brand.MCPServer, "--", e.Exe, "mcp"); err != nil {
				return nil, false, fmt.Errorf("`claude mcp add` failed: %v: %s", err, strings.TrimSpace(out))
			}
			did = append(did, "Claude Code: registered the "+brand.MCPServer+" MCP server for you, in every project ("+e.ClaudeJSON+")")
		default:
			// Claude Code is configured here but its command isn't on PATH, so write the entry the
			// command would have written.
			if servers == nil {
				servers = jsonedit.New()
				cfg.Set("mcpServers", servers)
			}
			e2 := jsonedit.New()
			e2.Set("type", "stdio")
			e2.Set("command", e.Exe)
			e2.Set("args", []any{"mcp"})
			e2.Set("env", jsonedit.New())
			servers.Set(brand.MCPServer, e2)
			out, err := jsonedit.Format(cfg, raw)
			if err == nil {
				err = writeAtomic(e.ClaudeJSON, out, 0o600)
			}
			if err != nil {
				return nil, false, fmt.Errorf("%s: %w", e.ClaudeJSON, err)
			}
			did = append(did, "Claude Code: registered the "+brand.MCPServer+" MCP server for you, in every project ("+e.ClaudeJSON+")")
		}
	}

	allowed, err := e.allowClaudeTools()
	if err != nil {
		return did, fresh, err
	}
	if allowed {
		did = append(did, "Claude Code: allowed "+brand.Name+"'s tools, so they don't ask each time ("+filepath.Join(e.ClaudeDir, "settings.json")+")")
	}
	return did, fresh, nil
}

// ownsEntry reports whether a Claude Code server entry runs this binary as t4's MCP server.
func ownsEntry(entry *jsonedit.Object, exe string) bool {
	if entry == nil {
		return false
	}
	cmd, _ := entry.Get("command")
	args, _ := entry.Get("args")
	list, _ := args.([]any)
	return cmd == exe && len(list) == 1 && list[0] == "mcp"
}

// allowClaudeTools adds the allow rule to the user's settings.json, and reports whether it was new.
func (e Env) allowClaudeTools() (bool, error) {
	path := filepath.Join(e.ClaudeDir, "settings.json")
	cfg, raw, err := jsonedit.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	perms := cfg.Object("permissions")
	if perms == nil {
		if _, taken := cfg.Get("permissions"); taken {
			return false, fmt.Errorf("%s: \"permissions\" isn't an object, so the allow rule wasn't added", path)
		}
		perms = jsonedit.New()
		cfg.Set("permissions", perms)
	}
	var allow []any
	if v, ok := perms.Get("allow"); ok {
		var isList bool
		if allow, isList = v.([]any); !isList {
			return false, fmt.Errorf("%s: \"permissions.allow\" isn't a list, so the allow rule wasn't added", path)
		}
	}
	for _, r := range allow {
		if r == allowRule {
			return false, nil
		}
	}
	perms.Set("allow", append(allow, allowRule))
	out, err := jsonedit.Format(cfg, raw)
	if err == nil {
		err = writeAtomic(path, out, 0o644)
	}
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return true, nil
}

func (e Env) runClaude(ctx context.Context, claude string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, claude, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// writeAtomic replaces path with data so a reader, or a crash, never sees half a file. The file
// keeps the permissions it had; a new one gets perm. A path that is a symbolic link (a dotfiles
// repository's, say) is written through: the file it points to is replaced, and the link stays.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".t4tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, perm)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
