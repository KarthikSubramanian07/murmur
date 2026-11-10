//go:build !windows

package jsonedit

import (
	"path/filepath"
	"syscall"
	"testing"
)

// A config file that is a named pipe is refused without being opened: opening one to read waits
// for a writer.
func TestReadFileRefusesAPipe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.local.json")
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	within(t, "ReadFile", func() {
		if _, _, err := ReadFile(path); err != errNotRegular {
			t.Errorf("ReadFile on a pipe: %v", err)
		}
	})
}
