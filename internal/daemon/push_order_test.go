package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A device pushes one ref at a time. GitHub refused now and then one of the two or three pushes a
// device made in the same second, as a session started or stopped (#141). The remote's pre-receive
// hook here records any push that arrives while another is being received, and holds each for a
// second, so that pushes made together would meet in it.
func TestPushesNeverOverlap(t *testing.T) {
	remote := newRemote(t)
	script := "#!/bin/sh\n" +
		"if ! mkdir \"$GIT_DIR/inpush\" 2>/dev/null; then echo overlap >> \"$GIT_DIR/overlaps\"; fi\n" +
		"cat >/dev/null\n" +
		"sleep 1\n" +
		"rmdir \"$GIT_DIR/inpush\" 2>/dev/null\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	a := newDevice(t, remote)
	dev := a.self()

	// A session starting pushes its name, its start on the device's log, and the device's presence.
	for _, id := range []string{"aaaaaa", "aaaaab", "aaaaac"} {
		addr, _ := a.session(id, "claude-"+id)
		name := a.named(addr)
		eventually(t, "the registry to have "+name, func() bool { return readRegistry(t, remote).holdsNames(name) })
	}
	eventually(t, "the sessions' starts on the bus", func() bool {
		return strings.Count(busText(t, remote, dev), " started (") == 3
	})
	if b, err := os.ReadFile(filepath.Join(remote, "overlaps")); err == nil {
		t.Fatalf("%d pushes arrived while another was being received", strings.Count(string(b), "overlap"))
	}
}
