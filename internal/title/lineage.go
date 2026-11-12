package title

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/a-Fig/murmur/internal/t3db"
)

// Lineage is who started a Codex thread, as Codex's state database records it (#454).
type Lineage struct {
	// Subagent says Codex started the thread for another thread: one a thread spawned, a review,
	// a guardian. threads.source is then a JSON object with a "subagent" key, where a thread a
	// person started has a plain word ("vscode", "exec").
	Subagent bool
	// Parent is the thread that spawned it (source's subagent.thread_spawn.parent_thread_id), ""
	// where Codex names none, as for a review.
	Parent string
	// Driven says the program that asked Codex for the thread is one that runs Codex threads as
	// another agent's subagents: threads.originator, the name the program gave Codex's app server,
	// is one of CodexDrivers.
	Driven bool
}

// Child says the thread is one another thread or agent started.
func (l Lineage) Child() bool { return l.Subagent || l.Driven }

// CodexDrivers are the originators that run Codex threads as another agent's subagents. Codex
// has no mark for that itself: to Codex such a program is a client like its desktop app
// ("Codex Desktop"), its terminal ("codex-tui"), `codex exec` ("codex_exec") or T3 Code. So the
// list is of names, and a new tool is off the mesh once its name is here. "codex-sub" is the
// Claude Code plugin of that name (598 of the 1,299 threads on Tyler's laptop, 2026-11-12).
var CodexDrivers = []string{"codex-sub"}

// ErrNoThread says Codex's state has no row for the thread, which it writes a moment after the
// thread starts.
var ErrNoThread = errors.New("Codex's state doesn't have the thread")

// CodexLineage reads the Lineage of thread id from the newest state_N.sqlite in dir, read-only.
// The originator column came to Codex later than source (it is in 0.166's state_5); a database
// without it gives a Lineage by source alone.
func CodexLineage(ctx context.Context, dir, id string) (Lineage, error) {
	if dir == "" || id == "" {
		return Lineage{}, errors.New("no Codex directory or thread to look up")
	}
	path := latestState(dir)
	if path == "" {
		return Lineage{}, errors.New("Codex's state database isn't there")
	}
	ctx, cancel := context.WithTimeout(ctx, Total)
	defer cancel()
	db, err := t3db.OpenReadOnly(path)
	if err != nil {
		return Lineage{}, err
	}
	defer db.Close()
	var source, originator sql.NullString
	err = db.QueryRowContext(ctx, `SELECT source, originator FROM threads WHERE id = ?`, id).Scan(&source, &originator)
	if err != nil && strings.Contains(err.Error(), "no such column: originator") {
		err = db.QueryRowContext(ctx, `SELECT source FROM threads WHERE id = ?`, id).Scan(&source)
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Lineage{}, ErrNoThread
	case err != nil:
		return Lineage{}, fmt.Errorf("reading Codex's database: %w", err)
	}
	l := Lineage{Driven: slices.Contains(CodexDrivers, originator.String)}
	l.Subagent, l.Parent = subagentSource(source.String)
	return l, nil
}

// subagentSource reads threads.source: whether it is a subagent's, and the thread that spawned
// it. A subagent's is {"subagent": ...}, where the value is a word ("review") or an object
// ({"thread_spawn": {"parent_thread_id": ...}}, {"other": "guardian"}); anything else, a plain
// word included, is a thread a person's client started.
func subagentSource(source string) (subagent bool, parent string) {
	var s struct {
		Subagent json.RawMessage `json:"subagent"`
	}
	if json.Unmarshal([]byte(source), &s) != nil || len(s.Subagent) == 0 || string(s.Subagent) == "null" {
		return false, ""
	}
	var how struct {
		Spawn struct {
			Parent string `json:"parent_thread_id"`
		} `json:"thread_spawn"`
	}
	_ = json.Unmarshal(s.Subagent, &how) // a word, which is no object, names no parent
	return true, how.Spawn.Parent
}
