package jsonedit

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// within fails the test if f hasn't returned in a few seconds: a read that follows a link to a
// device never ends by itself.
func within(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s is still reading after 10 s", what)
	}
}

// A config file that is a link to a regular file (a dotfiles repository's) is read through it.
func TestReadFileFollowsALinkToAFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real.json"), []byte(`{"a": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink("real.json", link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	o, raw, err := ReadFile(link)
	if err != nil || raw == nil {
		t.Fatalf("ReadFile through a link: %v", err)
	}
	if _, ok := o.Get("a"); !ok {
		t.Error("the file behind the link wasn't read")
	}
}

// A config file that is a link to a device (a repo can commit one as .claude/settings.local.json)
// is refused at once, not read until memory runs out.
func TestReadFileRefusesALinkToADevice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/zero on Windows")
	}
	link := filepath.Join(t.TempDir(), "settings.local.json")
	if err := os.Symlink("/dev/zero", link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	within(t, "ReadFile", func() {
		if _, _, err := ReadFile(link); err != errNotRegular {
			t.Errorf("ReadFile on a link to /dev/zero: %v", err)
		}
	})
}
