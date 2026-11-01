package join

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/gitx"
	"github.com/a-Fig/murmur/internal/jsonedit"
	"github.com/a-Fig/murmur/internal/setup"
)

// The notice hook goes in .claude/settings.local.json, the file Claude Code reads for one user's
// settings in one project and keeps out of commits. A teammate who hasn't joined has no such hook,
// and the one who has gets it in every working tree of the clone: they share one daemon.
const hookFile = ".claude/settings.local.json"

// worktrees lists the working trees of the clone, the one at place first.
func worktrees(ctx context.Context, p Place) []string {
	out, err := gitx.RunIn(ctx, p.Top, "worktree", "list", "--porcelain")
	trees := []string{p.Top}
	if err != nil {
		return trees
	}
	for _, line := range strings.Split(out, "\n") {
		path, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "worktree ")
		if !ok {
			continue
		}
		path = filepath.Clean(path)
		if path == p.Top || !isDir(path) || isBare(ctx, path) {
			continue
		}
		trees = append(trees, path)
	}
	return trees
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// isBare reports whether path is the bare repository entry of a worktree list, which has no
// working tree to put a file in.
func isBare(ctx context.Context, path string) bool {
	out, err := gitx.RunIn(ctx, path, "rev-parse", "--is-bare-repository")
	return err != nil || strings.TrimSpace(out) == "true"
}

// installHooks puts the notice hook in every working tree of the clone, and keeps the file out of
// commits. It returns the trees it changed.
func installHooks(ctx context.Context, p Place, exe string) (changed []string, err error) {
	if err := ignoreHookFile(p.GitDir); err != nil {
		return nil, err
	}
	for _, top := range worktrees(ctx, p) {
		did, err := installHook(top, setup.HookCommand(exe))
		if err != nil {
			return changed, err
		}
		if did {
			changed = append(changed, top)
		}
	}
	return changed, nil
}

// installHook adds the hook to one working tree's settings.local.json and reports whether that
// changed the file. A hook already there, with this command, is left as it is.
func installHook(top, command string) (bool, error) {
	path := filepath.Join(top, filepath.FromSlash(hookFile))
	cfg, raw, err := jsonedit.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	changed := false
	if hooks := cfg.Object("hooks"); hooks == nil {
		if _, taken := cfg.Get("hooks"); taken {
			return false, fmt.Errorf("%s: \"hooks\" isn't an object", path)
		}
	}
	// A hook of ours that names another path (the binary moved) is replaced, not doubled.
	if setup.RemoveHooksExcept(cfg, command) {
		changed = true
	}
	hooks := cfg.Object("hooks")
	if hooks == nil {
		hooks = jsonedit.New()
		cfg.Set("hooks", hooks)
	}
	for _, ev := range setup.HookEvents {
		event := ev.Name
		var groups []any
		if v, ok := hooks.Get(event); ok {
			var isList bool
			if groups, isList = v.([]any); !isList {
				return false, fmt.Errorf("%s: hooks.%s isn't a list", path, event)
			}
		}
		if hasCommand(groups, command) {
			continue
		}
		entry := jsonedit.New()
		entry.Set("type", "command")
		entry.Set("command", command)
		group := jsonedit.New()
		if ev.Matcher != "" {
			group.Set("matcher", ev.Matcher)
		}
		group.Set("hooks", []any{entry})
		hooks.Set(event, append(groups, group))
		changed = true
	}
	if !changed {
		return false, nil
	}
	out, err := jsonedit.Format(cfg, raw)
	if err == nil {
		err = writeAtomic(path, out, 0o644)
	}
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return true, nil
}

func hasCommand(groups []any, command string) bool {
	for _, g := range groups {
		group, _ := g.(*jsonedit.Object)
		list, _ := group.Get("hooks")
		entries, _ := list.([]any)
		for _, e := range entries {
			h, _ := e.(*jsonedit.Object)
			if c, _ := h.Get("command"); c == command {
				return true
			}
		}
	}
	return false
}

// removeHooks takes the hook out of every working tree of the clone and returns the trees it
// changed. A file that held only the hook is deleted, with its directory if that is then empty.
func removeHooks(ctx context.Context, p Place) (changed []string, err error) {
	for _, top := range worktrees(ctx, p) {
		path := filepath.Join(top, filepath.FromSlash(hookFile))
		cfg, raw, rerr := jsonedit.ReadFile(path)
		if rerr != nil {
			err = errors.Join(err, fmt.Errorf("%s: %w", path, rerr)) // unreadable: leave it, go on
			continue
		}
		if raw == nil || !setup.RemoveHooks(cfg) {
			continue
		}
		if cfg.Len() == 0 && !isLink(path) { // a link is the user's, and its target is written, not deleted
			if rmerr := os.Remove(path); rmerr != nil {
				err = errors.Join(err, rmerr)
				continue
			}
			os.Remove(filepath.Dir(path)) // only if nothing else is in it
		} else {
			out, ferr := jsonedit.Format(cfg, raw)
			if ferr == nil {
				ferr = writeAtomic(path, out, 0o644)
			}
			if ferr != nil {
				err = errors.Join(err, fmt.Errorf("%s: %w", path, ferr))
				continue
			}
		}
		changed = append(changed, top)
	}
	return changed, err
}

// ignoreHookFile lists the file in the clone's own ignore file, info/exclude, which every working
// tree consults and no commit contains.
func ignoreHookFile(gitDir string) error {
	path := filepath.Join(gitDir, "info", "exclude")
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "/"+hookFile {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	add := "/" + hookFile + "\n"
	if len(b) > 0 && b[len(b)-1] != '\n' {
		add = "\n" + add
	}
	if len(b) == 0 {
		add = "# " + brand.Product + ": the notice hook is this user's, not the repo's\n" + add
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(add)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
