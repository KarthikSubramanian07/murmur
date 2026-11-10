//go:build !windows

package setup

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A marker file that is a named pipe isn't opened: opening one to read waits for a writer, which
// would hang the MCP server's marker check and init.
func TestAMarkerFileThatIsAPipeIsNotRead(t *testing.T) {
	dir := repo(t, map[string]string{"CLAUDE.md": "# Notes\n"})
	if err := syscall.Mkfifo(filepath.Join(dir, "AGENTS.md"), 0o644); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	within(t, "the marker check", func() {
		if markedTree(dir) {
			t.Error("a pipe counts as marked")
		}
	})
	within(t, "init", func() {
		if _, err := Init(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Errorf("init on a pipe: %v", err)
		}
	})
}
