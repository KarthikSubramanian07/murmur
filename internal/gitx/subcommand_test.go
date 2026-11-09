package gitx

import "testing"

// Only commands harmless when run late may have a hung start given up on and run again (#217): a
// push or an update-ref must wait, whatever options come before it.
func TestOnlyCommandsHarmlessLateAreGivenUpOn(t *testing.T) {
	for _, c := range []struct {
		args []string
		safe bool
	}{
		{[]string{"--git-dir=/x", "-c", "gc.auto=0", "-c", "maintenance.auto=false", "fetch", "--quiet", "origin"}, true},
		{[]string{"--git-dir=/x", "commit-tree", "abc", "-p", "def"}, true},
		{[]string{"--git-dir=/x", "ls-remote", "origin"}, true},
		{[]string{"--git-dir=/x", "push", "--porcelain", "origin", "abc:refs/murmur/presence/d"}, false},
		{[]string{"--git-dir=/x", "update-ref", "refs/murmur/mesh/d", "abc"}, false},
		{[]string{"-c", "fetch", "push", "origin"}, false}, // -c takes the next word
		{[]string{"merge", "--ff-only", "abc"}, false},
		{[]string{"symbolic-ref", "HEAD", "refs/heads/main"}, false},
	} {
		if got := lateSafe[subcommand(c.args)]; got != c.safe {
			t.Errorf("%q: given up on is %v, want %v", c.args, got, c.safe)
		}
	}
}
