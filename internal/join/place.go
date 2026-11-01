// Package join is what makes Murmur start in a repo: the user's consent, kept per clone, and the
// per-user setup that goes with it (registering the MCP server with Claude Code and Codex, and the
// notice hook for this repo only). Nothing here is committed, and nothing starts without consent:
// the MCP server is registered for the user in every repo they open, so what keeps it from
// publishing their presence into repos that didn't invite it is Place.Active.
package join

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/a-Fig/murmur/internal/client"
	"github.com/a-Fig/murmur/internal/setup"
)

// consentFile is kept in the clone's murmur directory inside .git, which no commit, checkout or
// other user's clone has: joining is this user's, in this clone.
const consentFile = "joined.json"

// Place is a clone, as seen from one of its working trees.
type Place struct {
	Top    string // the root of this working tree
	GitDir string // the clone's git common dir, shared by all its worktrees
}

// Locate finds the clone containing dir.
func Locate(ctx context.Context, dir string) (Place, error) {
	top, err := setup.Top(ctx, dir)
	if err != nil {
		return Place{}, err
	}
	gitDir, err := client.FindGitDir(ctx, dir)
	if err != nil {
		return Place{}, err
	}
	return Place{Top: top, GitDir: gitDir}, nil
}

func (p Place) consentPath() string { return filepath.Join(client.StateDir(p.GitDir), consentFile) }

// Marked reports whether this working tree says the repo uses Murmur.
func (p Place) Marked() bool { return setup.Marked(p.Top) }

// Joined reports whether the user joined Murmur in this clone.
func (p Place) Joined() bool {
	if p.GitDir == "" {
		return false // no clone: the path below would be relative to wherever the process is
	}
	_, err := os.Stat(p.consentPath())
	return err == nil
}

// Joined reports whether the user joined Murmur in the clone with this git common dir. The daemon
// asks it, having no working tree of its own.
func Joined(gitDir string) bool { return Place{GitDir: gitDir}.Joined() }

// Active reports whether Murmur may run for this working tree: the repo invites it, and this user
// accepted. Both are checked from files alone, so it is cheap to ask again and again.
func (p Place) Active() bool { return p.Joined() && p.Marked() }

// RecordConsent notes that the user joined; it reports whether that was news. An existing record
// is left as it is, so joining again changes nothing.
func (p Place) RecordConsent() (bool, error) {
	if p.Joined() {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(p.consentPath()), 0o700); err != nil {
		return false, err
	}
	b, _ := json.Marshal(map[string]string{"joined": time.Now().UTC().Format(time.RFC3339)})
	return true, os.WriteFile(p.consentPath(), append(b, '\n'), 0o600)
}

// ForgetConsent removes the record, and reports whether there was one.
func (p Place) ForgetConsent() (bool, error) {
	err := os.Remove(p.consentPath())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
