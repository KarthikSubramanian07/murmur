package logfile

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// open opens path as a starter opens daemon.log for a daemon's standard error: to append.
func open(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A daemon that runs for days keeps its log under the limit, with what came before the last move
// in the .old file, and the lines after it at the start of the log, not after a hole where the old
// lines were.
func TestTheLogIsMovedAsideWhileTheDaemonRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	if err := os.WriteFile(path, []byte("from the daemon before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := log.New(New(open(t, path), path, 100), "", 0)
	for i := range 12 {
		l.Printf("line %02d, ten more bytes", i) // 25 bytes a line
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > 100 {
		t.Fatalf("the log is %d bytes, over the limit of 100", fi.Size())
	}
	// The file held 23 bytes, and takes four lines between moves.
	if got, want := read(t, path), lines(11, 11); got != want {
		t.Fatalf("the log after its last move is\n%q, want\n%q", got, want)
	}
	if got, want := read(t, path+".old"), lines(7, 10); got != want {
		t.Fatalf("the .old file is\n%q, want what came between the last two moves:\n%q", got, want)
	}
}

// lines are the lines the tests log, from line from to line to.
func lines(from, to int) (s string) {
	for i := from; i <= to; i++ {
		s += fmt.Sprintf("line %02d, ten more bytes\n", i)
	}
	return s
}

// A daemon whose standard error a shell opened with `2>`, not to append, writes at its own offset.
// Once the file is emptied it writes from the start again, rather than on after a hole of NUL bytes
// that would keep the file at its size and copy the hole into the .old file.
func TestALogNotOpenedToAppendIsMovedAsideToo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	l := log.New(New(f, path, 100), "", 0)
	for i := range 12 {
		l.Printf("line %02d, ten more bytes", i)
	}
	if got, want := read(t, path), lines(8, 11); got != want {
		t.Fatalf("the log after its last move is\n%q, want\n%q", got, want)
	}
	if got, want := read(t, path+".old"), lines(4, 7); got != want {
		t.Fatalf("the .old file is\n%q, want\n%q", got, want)
	}
}

// A daemon whose standard error isn't daemon.log, such as one run by hand in a terminal, leaves
// daemon.log alone, though another daemon wrote it.
func TestALogThatIsntTheDaemonsIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")
	big := strings.Repeat("x", 200)
	if err := os.WriteFile(path, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "terminal")
	w := New(open(t, other), path, 100)
	for range 10 {
		fmt.Fprintln(w, "a line of the daemon run by hand")
	}
	if got := read(t, path); got != big {
		t.Fatalf("daemon.log was changed to %q", got)
	}
	if _, err := os.Stat(path + ".old"); err == nil {
		t.Fatal("daemon.log was moved aside")
	}
	if got := strings.Count(read(t, other), "\n"); got != 10 {
		t.Fatalf("the daemon's own output has %d lines, want 10", got)
	}
}

// A daemon started after this one, by an update, moves daemon.log aside when it starts. This one,
// still writing to the file it had, then stops moving the log, which is now the new daemon's.
func TestALogMovedByTheNextDaemonIsTheNextDaemons(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	w := New(open(t, path), path, 100)
	fmt.Fprintln(w, "the old daemon starts")
	if err := os.Rename(path, path+".old"); err != nil {
		t.Skipf("can't rename an open file here: %v", err) // Windows; StartDaemon's rename fails there too
	}
	newer := "the new daemon's first line\n"
	if err := os.WriteFile(path, []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		fmt.Fprintln(w, "the old daemon's last lines, as it stops")
	}
	if got := read(t, path); got != newer {
		t.Fatalf("the new daemon's log was changed to %q", got)
	}
}

func TestCredentialsInURLsAreNotLogged(t *testing.T) {
	for in, want := range map[string]string{
		"fetching https://x-access-token:ghs_abc123@github.com/a-Fig/murmur.git failed": "fetching https://github.com/a-Fig/murmur.git failed",
		`remote "https://ghp_abc123@github.com/o/r"`:                                    `remote "https://github.com/o/r"`,
		"ssh://git@github.com:22/o/r and https://u:p@ss@host/x?y=a@b":                   "ssh://github.com:22/o/r and https://host/x?REDACTED",
		"git@github.com:o/r.git, mail a@b.c, https://github.com/o/r?u=a@b#c@d":          "git@github.com:o/r.git, mail a@b.c, https://github.com/o/r?REDACTED",
		`https://u:p"q@host/x and https://u:p<q>@host/y`:                                "https://host/x and https://host/y",
		"https://u:it's,secret@host/x":                                                  "https://host/x",
		"unable to access 'https://github.com/o/r.git?access_token=ghp_x/': refused":    "unable to access 'https://github.com/o/r.git?REDACTED refused",
		"page at http://127.0.0.1:7000/#token=abc open":                                 "page at http://127.0.0.1:7000/#REDACTED open",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q)\n = %q\nwant %q", in, got, want)
		}
	}

	path := filepath.Join(t.TempDir(), "daemon.log")
	l := log.New(New(open(t, path), path, Max), "", 0)
	l.Printf("reading refs/murmur/mesh/x: git ls-remote https://x-access-token:SECRET@github.com/o/r: fatal")
	if got := read(t, path); strings.Contains(got, "SECRET") || !strings.Contains(got, "https://github.com/o/r") {
		t.Fatalf("the log holds %q", got)
	}
}

// git puts a remote's URL in its own messages, query and all, and a token can be in the query. What
// git says when it can't reach such a remote reaches the log without the token.
func TestATokenInARemotesQueryIsNotLogged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal(err) // Murmur needs git, and so do its tests
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "http://127.0.0.1:1/repo?access_token=SENTINEL")
	// No user or system config, which could rewrite the URL, and no proxy, which could answer for it.
	empty := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+empty, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"http_proxy=", "https_proxy=", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "all_proxy=", "NO_PROXY=*", "no_proxy=*")
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Skip("something answered on port 1")
	}
	if !strings.Contains(string(out), "SENTINEL") {
		t.Fatalf("git didn't say the URL, so this checks nothing: %v, %q", err, out)
	}
	path := filepath.Join(t.TempDir(), "daemon.log")
	l := log.New(New(open(t, path), path, Max), "", 0)
	l.Printf("fetching: %s", out)
	if got := read(t, path); strings.Contains(got, "SENTINEL") {
		t.Fatalf("the log holds the token: %q", got)
	}
}

// One write bigger than MaxRecord, such as an error holding all of git's stderr, is cut, so it
// can't take the file past its limit on its own.
func TestOneBigWriteIsCut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	w := New(open(t, path), path, 4*MaxRecord)
	big := strings.Repeat("x", 10*MaxRecord) + "\n"
	if n, err := w.Write([]byte(big)); err != nil || n != len(big) {
		t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(big))
	}
	got := read(t, path)
	if len(got) > MaxRecord+100 || !strings.HasSuffix(got, " more bytes not logged)\n") {
		t.Fatalf("the log is %d bytes, ending %q", len(got), got[max(0, len(got)-60):])
	}

	// The cut falls between characters, not inside one.
	path = filepath.Join(t.TempDir(), "daemon.log")
	w = New(open(t, path), path, 4*MaxRecord)
	w.Write([]byte(strings.Repeat("x", MaxRecord-1) + "€€\n"))
	if got := read(t, path); !utf8.ValidString(got) || !strings.HasPrefix(got, strings.Repeat("x", MaxRecord-1)+"…") {
		t.Fatalf("the cut line isn't whole characters: ends %q", got[max(0, len(got)-60):])
	}
}

// When the log can't be emptied, a line that would take it past its limit is dropped, not written,
// and once it can be, a line says how much was dropped.
func TestALogThatCantBeEmptiedStaysAtItsLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	w := New(open(t, path), path, 100)
	failing := true
	w.truncate = func(p string, n int64) error {
		if failing {
			return errors.New("refused")
		}
		return os.Truncate(p, n)
	}
	l := log.New(w, "", 0)
	for i := range 12 {
		l.Printf("line %02d, ten more bytes", i)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() > 100 {
		t.Fatalf("the log grew past its limit while it couldn't be emptied: %v, %v", fi.Size(), err)
	}
	failing = false
	w.mu.Lock()
	w.retry = time.Time{} // the minute is up
	w.mu.Unlock()
	l.Printf("line %02d, ten more bytes", 12)
	if got := read(t, path); !strings.Contains(got, "bytes of log were dropped") || !strings.HasSuffix(got, lines(12, 12)) {
		t.Fatalf("once emptied, the log is %q", got)
	}
}

// When the .old file can't be written, the log is emptied anyway, so it stays under its limit, and
// its first line says what was lost.
func TestALogIsEmptiedWhenItsCopyFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	if err := os.Mkdir(path+".old", 0o700); err != nil { // a directory can't be opened to write
		t.Fatal(err)
	}
	l := log.New(New(open(t, path), path, 100), "", 0)
	for i := range 12 {
		l.Printf("line %02d, ten more bytes", i)
	}
	got := read(t, path)
	if !strings.Contains(got, "emptied without its copy") || !strings.HasSuffix(got, lines(11, 11)) {
		t.Fatalf("the log is %q", got)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() > 300 {
		t.Fatalf("the log grew past its limit: %v, %v", fi.Size(), err)
	}
}
