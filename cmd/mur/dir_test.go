package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// -C DIR before the command is taken as git takes it (#373), in each of the flag package's
// spellings, and as many times as given; what follows the command is left to it.
func TestLeadingDirs(t *testing.T) {
	for _, c := range []struct {
		args       []string
		dirs, rest []string
		bad        bool
	}{
		{args: []string{"stop"}, rest: []string{"stop"}},
		{args: []string{"-C", "a", "stop"}, dirs: []string{"a"}, rest: []string{"stop"}},
		{args: []string{"--C", "a", "ui", "--no-open"}, dirs: []string{"a"}, rest: []string{"ui", "--no-open"}},
		{args: []string{"-C=a", "-C", "b", "stop", "-C", "c"}, dirs: []string{"a", "b"}, rest: []string{"stop", "-C", "c"}},
		{args: []string{"-C", "a"}, dirs: []string{"a"}},
		{args: []string{"-C"}, bad: true},
		{args: []string{"-C="}, bad: true},
	} {
		dirs, rest, err := leadingDirs(c.args)
		if (err != nil) != c.bad {
			t.Errorf("%q: error %v", c.args, err)
			continue
		}
		if !slices.Equal(dirs, c.dirs) || !slices.Equal(rest, c.rest) {
			t.Errorf("%q: dirs %q, rest %q; want %q, %q", c.args, dirs, rest, c.dirs, c.rest)
		}
	}
}

// `mur -C DIR stop` acts on the repository at DIR, as `mur stop -C DIR` does, from a directory that
// isn't one; a relative DIR is taken from where mur runs.
func TestDirBeforeTheCommand(t *testing.T) {
	b := newBox(t)
	outside := filepath.Dir(b.clone)
	if out, code := b.run(outside, "stop"); code == 0 {
		t.Fatalf("mur stop outside a repository worked, so this tests nothing:\n%s", out)
	}
	for _, args := range [][]string{
		{"-C", b.clone, "stop"},
		{"-C", filepath.Base(b.clone), "stop"},
		{"stop", "-C", b.clone},
	} {
		out, code := b.run(outside, args...)
		if code != 0 || !strings.Contains(out, "No daemon is running") {
			t.Errorf("mur %s: exit %d\n%s", strings.Join(args, " "), code, out)
		}
	}
	if out, code := b.run(outside, "-C"); code != 2 || !strings.Contains(out, "needs a directory") {
		t.Errorf("mur -C: exit %d, want 2 and what is missing\n%s", code, out)
	}
}
