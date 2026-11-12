package title

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const lineageSchema = `CREATE TABLE threads (id TEXT PRIMARY KEY, source TEXT NOT NULL, originator TEXT)`

// The sources and originators below are as Codex 0.166 and 0.167 wrote them on Tyler's laptop
// (2026-11-12).
func TestCodexLineageReadsWhoStartedAThread(t *testing.T) {
	dir := t.TempDir()
	writeDB(t, filepath.Join(dir, "state_5.sqlite"), lineageSchema,
		`INSERT INTO threads VALUES ('terminal', 'vscode', 'codex-tui')`,
		`INSERT INTO threads VALUES ('exec', 'exec', 'codex_exec')`,
		`INSERT INTO threads VALUES ('old', 'vscode', NULL)`,
		`INSERT INTO threads VALUES ('driven', 'vscode', 'codex-sub')`,
		`INSERT INTO threads VALUES ('review', '{"subagent":"review"}', 'codex-sub')`,
		`INSERT INTO threads VALUES ('guardian', '{"subagent":{"other":"guardian"}}', 'Codex Desktop')`,
		`INSERT INTO threads VALUES ('spawned', '{"subagent":{"thread_spawn":{"parent_thread_id":"th-parent","depth":1,"agent_path":"/root/x","agent_nickname":"Raman","agent_role":null}}}', 'Codex Desktop')`,
		`INSERT INTO threads VALUES ('odd', '{"subagent":null}', 'Codex Desktop')`)
	ctx := context.Background()
	for id, want := range map[string]Lineage{
		"terminal": {},
		"exec":     {},
		"old":      {},
		"driven":   {Driven: true},
		"review":   {Subagent: true, Driven: true},
		"guardian": {Subagent: true},
		"spawned":  {Subagent: true, Parent: "th-parent"},
		"odd":      {},
	} {
		got, err := CodexLineage(ctx, dir, id)
		if err != nil || got != want {
			t.Errorf("CodexLineage(%s) = %+v, %v; want %+v", id, got, err, want)
		}
		if got.Child() != (want.Subagent || want.Driven) {
			t.Errorf("%s: Child() = %v", id, got.Child())
		}
	}
	if _, err := CodexLineage(ctx, dir, "nobody"); !errors.Is(err, ErrNoThread) {
		t.Errorf("a thread Codex doesn't have: %v, want ErrNoThread", err)
	}
	if _, err := CodexLineage(ctx, t.TempDir(), "terminal"); err == nil || errors.Is(err, ErrNoThread) {
		t.Errorf("a directory with no state: %v, want an error that isn't ErrNoThread", err)
	}
}

// A state database from before Codex recorded the originator still says a subagent is one.
func TestCodexLineageWithoutAnOriginatorColumn(t *testing.T) {
	dir := t.TempDir()
	writeDB(t, filepath.Join(dir, "state_5.sqlite"), `CREATE TABLE threads (id TEXT PRIMARY KEY, source TEXT NOT NULL)`,
		`INSERT INTO threads VALUES ('plain', 'vscode')`,
		`INSERT INTO threads VALUES ('review', '{"subagent":"review"}')`)
	ctx := context.Background()
	if got, err := CodexLineage(ctx, dir, "plain"); err != nil || got != (Lineage{}) {
		t.Errorf("plain: %+v, %v", got, err)
	}
	if got, err := CodexLineage(ctx, dir, "review"); err != nil || got != (Lineage{Subagent: true}) {
		t.Errorf("review: %+v, %v", got, err)
	}
	if _, err := CodexLineage(ctx, dir, "nobody"); !errors.Is(err, ErrNoThread) {
		t.Errorf("a thread Codex doesn't have: %v, want ErrNoThread", err)
	}
}

// Against a real Codex state, in MURMUR_REAL_CODEX_DIR, when MURMUR_REAL_CODEX_THREADS names
// threads to read, as "id=driven,id=subagent,id=person": the kind each must be read as.
func TestCodexLineageOfRealThreads(t *testing.T) {
	spec := os.Getenv("MURMUR_REAL_CODEX_THREADS")
	if spec == "" {
		t.Skip("MURMUR_REAL_CODEX_THREADS isn't set")
	}
	for _, pair := range strings.Split(spec, ",") {
		id, kind, _ := strings.Cut(pair, "=")
		got, err := CodexLineage(context.Background(), os.Getenv("MURMUR_REAL_CODEX_DIR"), id)
		if err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		t.Logf("%s: %+v", id, got)
		ok := map[string]bool{"driven": got.Driven, "subagent": got.Subagent, "person": !got.Child()}[kind]
		if !ok {
			t.Errorf("%s was read as %+v, want %s", id, got, kind)
		}
	}
}
