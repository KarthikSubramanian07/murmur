package clones

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A clone recorded by join, then by its daemon with the repository's name, is listed once with that
// name; one whose folder is gone, or whose user has left, is dropped from the record as it is read.
func TestTheRecordListsJoinedClonesThatAreStillThere(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "record")
	root := t.TempDir()
	gitDir := func(name string) string {
		g := filepath.Join(root, name, ".git")
		if err := os.MkdirAll(g, 0o700); err != nil {
			t.Fatal(err)
		}
		return g
	}
	a, b, gone, left := gitDir("a"), gitDir("b"), gitDir("gone"), gitDir("left")
	for _, c := range []Clone{{GitDir: a}, {GitDir: b, Name: "murmur"}, {GitDir: gone}, {GitDir: left}} {
		if err := Add(dir, c); err != nil {
			t.Fatal(err)
		}
	}
	// The daemon names a; a join afterwards keeps the name.
	if err := Add(dir, Clone{GitDir: a, Name: "healveda"}); err != nil {
		t.Fatal(err)
	}
	if err := Add(dir, Clone{GitDir: a}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(gone)); err != nil {
		t.Fatal(err)
	}
	joined := func(g string) bool { return g != left }

	got, err := List(dir, joined)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].GitDir != a || got[0].Label() != "healveda" || got[1].GitDir != b || got[1].Label() != "murmur" {
		t.Fatalf("listed %+v; want healveda (a) and murmur (b)", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("the record keeps %d files; the deleted and the left clones should be off it", len(entries))
	}

	// A leave takes the clone off; a second is no error.
	if err := Remove(dir, b); err != nil {
		t.Fatal(err)
	}
	if err := Remove(dir, b); err != nil {
		t.Fatal(err)
	}
	if got, _ := List(dir, joined); len(got) != 1 || got[0].GitDir != a {
		t.Errorf("after the leave, listed %+v", got)
	}
}

// No record yet is an empty list, not an error; a file the package didn't write is dropped.
func TestAnEmptyOrStrangeRecord(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "record")
	if got, err := List(dir, func(string) bool { return true }); err != nil || len(got) != 0 {
		t.Fatalf("listed %v, %v from no record", got, err)
	}
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "0123456789abcdef.json"), []byte(`{"gitDir":"relative/.git"}`), 0o600)
	os.WriteFile(filepath.Join(dir, ".clone-123"), []byte(`{"gitDir":"/half"}`), 0o600) // being written
	if got, _ := List(dir, func(string) bool { return true }); len(got) != 0 {
		t.Fatalf("listed %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "0123456789abcdef.json")); err == nil {
		t.Error("a record that names no absolute git dir is kept")
	}
	if _, err := os.Stat(filepath.Join(dir, ".clone-123")); err != nil {
		t.Error("a file being written was removed")
	}
}

// A clone without a name goes by its folder.
func TestLabel(t *testing.T) {
	c := Clone{GitDir: filepath.Join(t.TempDir(), "t4", ".git")}
	if c.Label() != "t4" {
		t.Errorf("Label() = %q", c.Label())
	}
	if c.Top() != filepath.Dir(c.GitDir) {
		t.Errorf("Top() = %q", c.Top())
	}
}

// On Windows one folder can be written with either case of drive letter.
func TestIDIgnoresCaseOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("paths' case matters here")
	}
	if ID(`c:\Code\repo\.git`) != ID(`C:\code\repo\.git`) {
		t.Error("the same folder got two IDs")
	}
}

func TestNameOf(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/a-Fig/murmur.git":  "murmur",
		"https://github.com/a-Fig/murmur":      "murmur",
		"https://github.com/a-Fig/murmur.git/": "murmur",
		"git@github.com:a-Fig/murmur.git":      "murmur",
		"ssh://git@github.com/a-Fig/murmur":    "murmur",
		"/srv/git/murmur.git":                  "murmur",
		`C:\srv\murmur.git`:                    "murmur",
		"../remote.git":                        "remote",
		"":                                     "",
	} {
		if got := NameOf(in); got != want {
			t.Errorf("NameOf(%q) = %q, want %q", in, got, want)
		}
	}
	if strings.Contains(NameOf("https://example.com/"), "/") {
		t.Error("a name with a slash")
	}
}
