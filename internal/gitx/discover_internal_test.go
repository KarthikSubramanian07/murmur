package gitx

import (
	"io/fs"
	"os"
	"testing"
	"time"
)

// irregularDir is how Lstat describes a reparse point that isn't a link, such as a OneDrive
// files-on-demand folder on Windows, which no test can make.
type irregularDir struct{ name string }

func (i irregularDir) Name() string       { return i.name }
func (i irregularDir) Size() int64        { return 0 }
func (i irregularDir) Mode() fs.FileMode  { return fs.ModeDir | fs.ModeIrregular }
func (i irregularDir) ModTime() time.Time { return time.Time{} }
func (i irregularDir) IsDir() bool        { return true }
func (i irregularDir) Sys() any           { return nil }

// A folder that Go calls irregular but that isn't a link or a junction leads nowhere else, so
// Discover goes on past it rather than ask git, which would cost the hook a git process before
// every tool call in a checkout kept in OneDrive.
func TestIrregularFolderIsNoLink(t *testing.T) {
	dir := t.TempDir()
	if redirects(dir, irregularDir{name: "plain"}) {
		t.Error("an irregular folder that os.Readlink can't read was taken for a link")
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if redirects(dir, fi) {
		t.Error("a plain folder was taken for a link")
	}
}
