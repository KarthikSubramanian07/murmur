// Package logfile is what a daemon's log passes through on its way to daemon.log. It keeps the
// file bounded while the daemon runs, and keeps credentials in URLs out of it, since daemon.log is
// what a teammate is asked to send when their device misbehaves (docs/VISION.md, "One daemon per
// device").
package logfile

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"sync"
)

// Max is the size past which daemon.log moves to daemon.log.old: when a daemon starts
// (client.StartDaemon), and while it runs (Writer).
const Max = 4 << 20

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

// Write writes p, with any URL's credentials taken out, after moving the file aside if p would
// take it past the limit. A log.Logger writes each line in one call, so a URL is never split
// between two.
func (w *Writer) Write(p []byte) (int, error) {
	n := len(p)
	if bytes.Contains(p, []byte("://")) {
		p = []byte(Redact(string(p)))
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.path != "" && w.size > 0 && w.size+int64(len(p)) > w.max {
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
// empty the file the new daemon writes. A copy that fails leaves the file as it is, to be tried
// again after another max bytes. What reaches the file other than through this Writer while it is
// copied (a panic's trace, a starter's line) is lost when it is emptied. Caller holds mu.
func (w *Writer) rotate() {
	w.size = 0
	if _, ok := same(w.out, w.path); !ok {
		w.path = ""
		return
	}
	if copyFile(w.path, w.path+".old") != nil {
		return
	}
	if os.Truncate(w.path, 0) == nil {
		w.out.Seek(0, io.SeekStart)
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
// authority's last @, since a password can hold an @ of its own. It stops at a double quote or an
// angle bracket, which no URL holds unescaped, so `"https://host","a@b"` keeps its host. A comma
// or a single quote doesn't stop it: a password may hold either, and stopping there would let the
// whole password through.
var userinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^\s/?#"<>]*@`)

// Redact takes the user and password out of every URL in s, as git does in its own messages. A
// remote's URL can carry a token, and a log line can hold one from git's stderr or an error. It
// takes out only those: a token in a query string (`?access_token=…`) stays, and nothing the
// daemon logs carries one, since the page is logged by its address alone and the remote by name.
func Redact(s string) string {
	return userinfo.ReplaceAllString(s, "$1")
}
