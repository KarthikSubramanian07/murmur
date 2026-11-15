package bus_test

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/gitx"
)

// A file's name becomes a name on the receiver's disk, inside a directory Murmur chose, so no name
// a sender writes can be a path, hidden, or one Windows keeps for a device.
func TestFileName(t *testing.T) {
	for in, want := range map[string]string{
		"shot.png":                        "shot.png",
		"../../etc/passwd":                "passwd",
		`C:\Users\ada\notes.txt`:          "notes.txt",
		"a b\tc.log":                      "a_b_c.log",
		".env":                            "env",
		"...":                             "file",
		"":                                "file",
		"con.txt":                         "_con.txt",
		"COM1":                            "_COM1",
		"comet.txt":                       "comet.txt",
		"trailing.":                       "trailing",
		"résumé.pdf":                      "résumé.pdf",
		"x\x00y\x1b[31m.txt":              "x_y__31m.txt",
		strings.Repeat("a", 200):          strings.Repeat("a", 60),
		strings.Repeat("b", 200) + ".png": strings.Repeat("b", 56) + ".png",
	} {
		if got := bus.FileName(in); got != want {
			t.Errorf("FileName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A file's blob opens only as the file its event describes, at its place in its message.
func TestSealedFileOpensOnlyAsItself(t *testing.T) {
	plain := []byte("line one\r\nline two\x00\x1a\xff")
	blob, f, err := bus.SealFile(plain, "0123456789abcdef", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := bus.OpenFile(blob, f, "0123456789abcdef", 1); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("opened %q, %v", got, err)
	}
	if bytes.Contains(blob, []byte("line one")) {
		t.Fatal("the blob holds the file in the clear")
	}
	flipped := bytes.Clone(blob)
	flipped[len(flipped)-1] ^= 1
	smaller := f
	smaller.Size--
	other := f
	other.Sum = strings.Repeat("0", 64)
	for what, open := range map[string]func() ([]byte, error){
		"at another place":     func() ([]byte, error) { return bus.OpenFile(blob, f, "0123456789abcdef", 0) },
		"in another message":   func() ([]byte, error) { return bus.OpenFile(blob, f, "fedcba9876543210", 1) },
		"with a byte changed":  func() ([]byte, error) { return bus.OpenFile(flipped, f, "0123456789abcdef", 1) },
		"cut short":            func() ([]byte, error) { return bus.OpenFile(blob[:20], f, "0123456789abcdef", 1) },
		"said to be smaller":   func() ([]byte, error) { return bus.OpenFile(blob, smaller, "0123456789abcdef", 1) },
		"said to be different": func() ([]byte, error) { return bus.OpenFile(blob, other, "0123456789abcdef", 1) },
	} {
		if got, err := open(); err == nil {
			t.Errorf("a blob %s opened: %q", what, got)
		}
	}
}

// What a reader takes of a message's files is held to what Murmur writes. A file it can't take
// keeps its place, since blobs are named by place.
func TestCleanFiles(t *testing.T) {
	_, good, err := bus.SealFile([]byte("x"), "0123456789abcdef", 0)
	if err != nil {
		t.Fatal(err)
	}
	named := func(name string) bus.File { f := good; f.Name = name; return f }
	huge := named("huge.bin")
	huge.Size = bus.MaxFile + 1
	e := bus.Event{Type: bus.Message, Files: []bus.File{
		named("a.txt"), named("../A.TXT"), {Name: "nokey.txt", Size: 1}, huge, named("a.txt"),
	}}
	for range bus.MaxFiles {
		e.Files = append(e.Files, named("more"))
	}
	e.Clean()
	var names []string
	for _, f := range e.Files {
		names = append(names, f.Name)
	}
	if got, want := strings.Join(names, " "), "a.txt 2-A.TXT nokey.txt huge.bin 3-a.txt more 2-more 3-more"; got != want {
		t.Errorf("names: %s\nwant:  %s", got, want)
	}
	if e.Files[0].Readable() != true || e.Files[2].Readable() || e.Files[3].Readable() {
		t.Errorf("readable: %+v", e.Files[:4])
	}
	if e.Files[3].Size != 0 {
		t.Errorf("a file over the limit kept its size: %+v", e.Files[3])
	}
	started := bus.Event{Type: bus.SessionStarted, Files: []bus.File{named("a.txt")}}
	started.Clean()
	if len(started.Files) != 0 {
		t.Errorf("an event that isn't a message kept files: %+v", started.Files)
	}
}

// Blobs go through git as they are, whatever their bytes, and come back by their place. A blob
// over the limit it is read with isn't read.
func TestFilesCommit(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	ctx := context.Background()
	r := &bus.Repo{Git: gitx.Git{Dir: dir}, Name: "test", Email: "test@example.invalid"}
	every := make([]byte, 0, 512)
	for range 2 {
		for b := range 256 {
			every = append(every, byte(b))
		}
	}
	blobs := [][]byte{[]byte("text\r\nwith both\nline ends\r\n"), every, bytes.Repeat([]byte{0}, 3000)}
	sha, err := r.NewFilesCommit(ctx, blobs)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range blobs {
		got, err := r.ReadFileBlob(ctx, sha, i, 4000)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("blob %d: %d bytes, %v; want %d", i, len(got), err, len(want))
		}
	}
	if _, err := r.ReadFileBlob(ctx, sha, 3, 4000); !errors.Is(err, bus.ErrNoFile) {
		t.Errorf("a place with no blob: %v", err)
	}
	if _, err := r.ReadFileBlob(ctx, sha, 2, 2999); !errors.Is(err, bus.ErrTooLarge) {
		t.Errorf("a blob over the limit: %v", err)
	}
	if got, err := exec.Command("git", "--git-dir="+dir, "for-each-ref").Output(); err != nil || len(got) != 0 {
		t.Errorf("writing files left refs: %q, %v", got, err)
	}
}
