package join

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// npm installs OpenCode on Windows as opencode.cmd, which Go runs through cmd.exe itself: join
// reads its version from one (Sol's review of #396 thought it couldn't).
func TestRunProgramRunsACmdShim(t *testing.T) {
	shim := filepath.Join(t.TempDir(), "opencode.cmd")
	if err := os.WriteFile(shim, []byte("@echo opencode v2.2.0\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runProgram(context.Background(), shim, "--version")
	if err != nil || strings.TrimSpace(out) != "opencode v2.2.0" {
		t.Fatalf("out %q, err %v", out, err)
	}
	e := Env{Run: runProgram}
	if s := e.openCodeShapeFor(context.Background(), agentHome{Bin: shim}); !s.v2() {
		t.Error("a 2.2.0 shim: not OpenCode 2")
	}
}
