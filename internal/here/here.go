// Package here lets a session say which repo it works in, when that isn't where it started (#200).
//
// The MCP server picks its repo from the directory its agent started it in. A session that started
// in a parent folder, the home directory or a workspace that holds several repos, and then works in
// one of them, would otherwise never get a name there. So each MCP server publishes a small record
// of itself in a per-user directory, and `mur join`, run by the agent, finds the record of its own
// session by process (the agent's process is the server's parent, and an ancestor of the command)
// and leaves a request for that server to move to the repo it joined. The server looks for one
// every time it looks for a join or a leave.
//
// A session is in one repo at a time: the tools take no repo, so a session in two meshes would
// need one on every call and every message. Moving leaves the repo it was in.
package here

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
)

// Session is what an MCP server says about itself.
type Session struct {
	ServerPID     int    `json:"serverPid"`
	ClientPID     int    `json:"clientPid"`               // its parent: the agent's process
	ClaudeSession string `json:"claudeSession,omitempty"` // Claude Code's session ID, as the server started with it
	Agent         string `json:"agent,omitempty"`
	GitDir        string `json:"gitDir,omitempty"` // the clone it is placed in, "" if none
	On            bool   `json:"on,omitempty"`     // registered with that clone's daemon, tools listed
}

// Move asks the server of the agent process ClientPID to work in Dir.
type Move struct {
	ClientPID int `json:"clientPid"`
	// ClaudeSession is the session's, as its server published it; a server with another one, which
	// would be a new process that took a dead one's ID, ignores the move.
	ClaudeSession string    `json:"claudeSession,omitempty"`
	Dir           string    `json:"dir"`
	At            time.Time `json:"at"`
}

// DefaultDir is the user's: in their config directory, beside the check-in's ID, unless
// brand.SessionsDirEnv names another.
func DefaultDir() (string, error) {
	if dir := os.Getenv(brand.SessionsDirEnv); dir != "" {
		return dir, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, brand.Name, "sessions"), nil
}

func sessionPath(dir string, serverPID int) string {
	return filepath.Join(dir, "server-"+strconv.Itoa(serverPID)+".json")
}

func movePath(dir string, clientPID int) string {
	return filepath.Join(dir, "move-"+strconv.Itoa(clientPID)+".json")
}

// Publish writes the server's record, replacing its last one.
func Publish(dir string, s Session) error {
	return writeJSON(sessionPath(dir, s.ServerPID), s)
}

// Withdraw removes the server's record, as it exits.
func Withdraw(dir string, serverPID int) { os.Remove(sessionPath(dir, serverPID)) }

// Sessions are the records of servers still running. Records and moves of processes that have
// exited are removed on the way.
func Sessions(dir string) ([]Session, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Session
	for _, e := range entries {
		name := e.Name()
		path := filepath.Join(dir, name)
		switch {
		case strings.HasPrefix(name, "server-"):
			var s Session
			if readJSON(path, &s) != nil || !alive(s.ServerPID) {
				os.Remove(path)
				continue
			}
			out = append(out, s)
		case strings.HasPrefix(name, "move-"):
			var m Move
			if readJSON(path, &m) != nil || !alive(m.ClientPID) {
				os.Remove(path)
			}
		}
	}
	return out, nil
}

// Caller picks, from the running servers, the one of the session a command runs in: the one whose
// agent is claudePID (Claude Code's CLAUDE_PID, in the environment of the commands it runs), or
// else whose agent is the nearest of the command's ancestors. Where one agent process runs several
// sessions (Codex's app server in T3), claudeSession tells them apart if it is known; otherwise
// they all come back, and the caller can't say which is its own.
func Caller(sessions []Session, ancestors []int, claudePID int, claudeSession string) []Session {
	pids := ancestors
	if claudePID != 0 {
		pids = append([]int{claudePID}, ancestors...)
	}
	for _, pid := range pids {
		var found []Session
		for _, s := range sessions {
			if s.ClientPID == pid {
				found = append(found, s)
			}
		}
		if len(found) > 1 && claudeSession != "" {
			found = slices.DeleteFunc(found, func(s Session) bool { return s.ClaudeSession != claudeSession })
		}
		if len(found) > 0 {
			return found
		}
	}
	return nil
}

// Ask leaves the move for the session's server to find.
func Ask(dir string, s Session, workDir string, now time.Time) error {
	return writeJSON(movePath(dir, s.ClientPID), Move{ClientPID: s.ClientPID, ClaudeSession: s.ClaudeSession, Dir: workDir, At: now})
}

// Asked is the move left for the server of agent process clientPID, if there is one for it.
func Asked(dir string, clientPID int, claudeSession string) (Move, bool) {
	var m Move
	if readJSON(movePath(dir, clientPID), &m) != nil || m.ClientPID != clientPID || m.Dir == "" {
		return Move{}, false
	}
	if m.ClaudeSession != claudeSession {
		return Move{}, false
	}
	return m, true
}

// Ancestors are the command's ancestors, its parent first.
func Ancestors() []int {
	parents, err := parentsOf()
	if err != nil {
		return nil
	}
	var out []int
	seen := map[int]bool{os.Getpid(): true}
	for pid := parents[os.Getpid()]; pid > 0 && !seen[pid] && len(out) < 64; pid = parents[pid] {
		seen[pid] = true
		out = append(out, pid)
	}
	return out
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	// Written whole, then renamed, so a reader never sees half of one.
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
