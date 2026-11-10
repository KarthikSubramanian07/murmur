// Package logfile is what a daemon's log passes through on its way to daemon.log. It keeps the
// file bounded while the daemon runs, and keeps credentials in URLs out of it, since daemon.log is
// what a teammate is asked to send when their device misbehaves (docs/VISION.md, "One daemon per
// device").
package logfile

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"sync"
	"time"
)

// Max is the size past which daemon.log moves to daemon.log.old: when a daemon starts
// (client.StartDaemon), and while it runs (Writer).
const Max = 4 << 20

// MaxRecord is the most of one write that reaches the file; the rest is cut, with a note of how
// much. git's stderr is kept whole in an error, and one error that size would take the file past
// Max on its own, and the .old file after it.
const MaxRecord = 64 << 10

// Writer writes a daemon's log to out, its standard error, which a starter set to daemon.log (or
// the daemon did, for --log). Once the file would pass a size, Writer copies it to path+".old" and
// empties it. Renaming it would be simpler, but Windows doesn't rename a file that is open, and
// out stays open: it is the process's standard error, which a panic's trace goes to too.
// After emptying the file, Writer moves out's offset back to its start. Both starters open the
// file to append, so their writes go to its end wherever that now is, but a daemon whose stderr a
// shell opened with `2>` would write on at the old offset, behind a hole of NUL bytes.
type Writer struct {
	mu   sync.Mutex
	out  *os.File
	path string // "" when out isn't the file at path, such as a terminal: then nothing is moved
	size int64
	max  int64
	// retry is when to try moving the file aside again, after emptying it failed.
	retry time.Time
}

// New returns a Writer to out that keeps the file at path under max bytes, if out is that file.
func New(out *os.File, path string, max int64) *Writer {
	w := &Writer{out: out, max: max}
	if fi, ok := same(out, path); ok {
		w.path, w.size = path, fi.Size()
	}
	return w
}

// same says out is the file at path, and returns what out's Stat found.
func same(out *os.File, path string) (os.FileInfo, bool) {
	ofi, err := out.Stat()
	if err != nil {
		return nil, false
	}
	pfi, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	return ofi, os.SameFile(ofi, pfi)
}

// Write writes p, with any URL's credentials taken out and cut to MaxRecord, after moving the file
// aside if p would take it past the limit. A log.Logger writes each line in one call, so a URL is
// never split between two.
func (w *Writer) Write(p []byte) (int, error) {
	n := len(p)
	if bytes.Contains(p, []byte("://")) {
		p = []byte(Redact(string(p)))
	}
	if len(p) > MaxRecord {
		p = append(p[:MaxRecord:MaxRecord], fmt.Sprintf("… (%d more bytes not logged)\n", len(p)-MaxRecord)...)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.path != "" && w.size > 0 && w.size+int64(len(p)) > w.max && !time.Now().Before(w.retry) {
		w.rotate()
	}
	m, err := w.out.Write(p)
	w.size += int64(m)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// rotate copies the file to path+".old" and empties it. A daemon started after this one, by an
// update, moves the file aside itself when it starts; this one then stops moving it, rather than
// empty the file the new daemon writes. A copy that fails doesn't stop the emptying: the limit
// holds, what the file held is lost, and a line at its start says so. Emptying that fails leaves the
// size as it is and tries again in a minute, not on every line, since each try copies the file.
// What reaches the file other than through this Writer while it is copied (a panic's trace, a
// starter's line) is lost when it is emptied. Caller holds mu.
func (w *Writer) rotate() {
	if _, ok := same(w.out, w.path); !ok {
		w.path, w.size = "", 0
		return
	}
	copyErr := copyFile(w.path, w.path+".old")
	if err := os.Truncate(w.path, 0); err != nil {
		w.retry = time.Now().Add(time.Minute)
		return
	}
	w.out.Seek(0, io.SeekStart)
	w.size = 0
	if copyErr != nil {
		m, _ := fmt.Fprintf(w.out, "%s the log was emptied without its copy to %s.old: %v\n",
			time.Now().Format("2006/01/02 15:04:05"), w.path, copyErr)
		w.size += int64(m)
	}
}

func copyFile(from, to string) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

// userinfo is the user and password of a URL (`https://x-access-token:…@github.com/…`), up to its
// authority's last @, since a password can hold an @ of its own. Nothing but a space, /, ? or #
// stops it: git takes a remote typed with a quote, a bracket, a comma or an apostrophe in its
// password, and stopping at any of those would leave no @ to match and let the password through.
// The price is that text such as `"https://host","a@b"` loses more than it needs to.
var userinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^\s/?#]*@`)

// afterPath is a URL's query and fragment, up to the next space. A remote's URL can carry a token in
// its query (`?access_token=…`), and git puts the URL in its own messages, query and all; the page's
// address carries its token in its fragment (`#token=…`).
var afterPath = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://[^\s?#]*)([?#])[^\s]*`)

// Redact takes the user and password out of every URL in s, as git does in its own messages, and
// replaces its query and fragment with REDACTED. A remote's URL can carry a token in either place,
// and a log line can hold one from git's stderr or an error. Whatever followed the URL up to the
// next space, such as a closing quote, goes with its query.
func Redact(s string) string {
	s = userinfo.ReplaceAllString(s, "$1")
	return afterPath.ReplaceAllString(s, "${1}${2}REDACTED")
}
