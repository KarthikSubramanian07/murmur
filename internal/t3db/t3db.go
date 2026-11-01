// Package t3db finds the T3 Code thread a coding agent's session belongs to, in T3's own state
// database. T3 gives the agents it runs no thread ID: an MCP server has T3CODE_CLI_PATH in its
// environment and nothing else to say which thread it is in. The database does. In
// orchestration_v2_projection_provider_threads, each thread is tied to the session its provider
// runs it with, and payload_json.nativeThreadRef.nativeId is that session's own ID: the Claude
// Code session ID or the Codex thread ID, next to the thread's ID (seen on T3 0.1.x, 2026-10-29).
//
// The database is T3's internal state and may change in any update, and T3 has it open for
// writing at the same moment. So the lookup only reads, in SQLite's read-only mode (never
// immutable, which would ignore the write-ahead log and miss the newest threads), it waits no
// longer than a moment for a lock, and any trouble is an error for the caller to shrug off.
//
// The reader is modernc.org/sqlite: SQLite itself, translated to Go, so it needs no C compiler
// and cross-compiles like the rest of Murmur, and its file locking and shared-memory index for the
// write-ahead log are SQLite's own, which matters when another process has the file open. It adds
// about 6 MB to the binary (t4.exe for Windows, 17.7 MB to 23.8 MB, 2026-10-29); ncruces/go-sqlite3,
// which runs SQLite as WebAssembly, added 10.5 MB on a probe and brings locking of its own.
package t3db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/a-Fig/murmur/internal/t3"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// ErrNotFound means the database has no thread for the session.
var ErrNotFound = errors.New("T3's database has no thread for this session")

// Timeouts, variables so tests needn't wait them out. Busy is how long SQLite waits on a lock the
// writer holds; Total bounds the whole lookup.
var (
	Busy  = 1500 * time.Millisecond
	Total = 4 * time.Second
)

// threadQuery finds the thread of a native session ID. A row whose payload isn't JSON, or has no
// native ID, is passed over rather than failing the query for everyone. The thread_id column is
// what T3 fills in; the payload's appThreadId is the same thing, for a row that left the column
// empty. A session in more than one row, resumed under another thread or an ID reused, takes the
// most recently updated.
const threadQuery = `
SELECT COALESCE(thread_id, json_extract(payload_json, '$.appThreadId'))
FROM orchestration_v2_projection_provider_threads
WHERE (CASE WHEN json_valid(payload_json) THEN json_extract(payload_json, '$.nativeThreadRef.nativeId') END) = ?
ORDER BY updated_at DESC
LIMIT 1`

// ThreadOf finds the T3 thread whose provider session is native: a Claude Code session ID or a
// Codex thread ID. cli is T3CODE_CLI_PATH and home T3CODE_HOME; either may be empty, and they say
// where T3's data directory is. It reads and writes nothing else.
func ThreadOf(ctx context.Context, cli, home, native string) (string, error) {
	if native == "" {
		return "", errors.New("no session ID to look up")
	}
	var thread sql.NullString
	err := queryOne(ctx, cli, home, threadQuery, native, &thread)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", ErrNotFound
	case err != nil:
		return "", err
	case !thread.Valid || thread.String == "":
		return "", ErrNotFound
	}
	return thread.String, nil
}

// titleQuery reads a thread's title, the one T3 shows for it in its sidebar (seen on T3 0.1.x,
// 2026-10-30; Codex's own name for the conversation is a different field, and T3 never copies the
// title into it).
const titleQuery = `SELECT title FROM orchestration_v2_projection_threads WHERE thread_id = ?`

// TitleOf reads the title of T3 thread, as ThreadOf reads its row: read-only, with a short wait on
// a lock, any trouble an error. "" with no error means the thread has no title yet.
func TitleOf(ctx context.Context, cli, home, thread string) (string, error) {
	if thread == "" {
		return "", errors.New("no thread to look up")
	}
	var title sql.NullString
	err := queryOne(ctx, cli, home, titleQuery, thread, &title)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", ErrNotFound
	case err != nil:
		return "", err
	}
	return title.String, nil
}

// queryOne runs a query with one argument against T3's state database and scans its first row.
func queryOne(ctx context.Context, cli, home, query, arg string, dest any) error {
	var path string
	for _, h := range t3.Homes(cli, home) {
		p := t3.StateDB(h)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			path = p
			break
		}
	}
	if path == "" {
		return errors.New("T3's state database isn't there")
	}
	ctx, cancel := context.WithTimeout(ctx, Total)
	defer cancel()
	db, err := OpenReadOnly(path)
	if err != nil {
		return err
	}
	defer db.Close()
	err = db.QueryRowContext(ctx, query, arg).Scan(dest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("reading T3's database: %w", err)
	}
	return err
}

// OpenReadOnly opens a SQLite database to read only, waiting at most Busy on a lock: for T3's
// database, and for other agents' state that Murmur reads the same way (package title). It opens in
// SQLite's read-only mode, never immutable, and refuses writes. The caller closes it and bounds its
// queries with a context.
func OpenReadOnly(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// dsn is the URI that opens path read-only, waiting at most Busy on a lock. The path is escaped
// so that a space, a % or a ? in it is read as part of the path.
func dsn(path string) string {
	u := fileURL(path)
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", Busy.Milliseconds()))
	q.Add("_pragma", "query_only(1)")
	u.RawQuery = q.Encode()
	return u.String()
}

// fileURL is the file: URI of path. Left as a plain name, a path with a ? in it would be cut there
// by the driver, which reads what follows as the query.
func fileURL(path string) url.URL {
	slashed := filepath.ToSlash(path)
	if len(slashed) > 1 && slashed[1] == ':' {
		slashed = "/" + slashed // a Windows drive letter: file:///C:/...
	}
	return url.URL{Scheme: "file", Path: slashed}
}
