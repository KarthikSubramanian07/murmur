package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A session's start and stop that fail to push are written once the remote takes pushes to the
// device's log again, each once and in the order they happened (#434). They were logged and
// dropped: the page's Recent view never showed the session joining or leaving. The remote's
// pre-receive hook here refuses every push to the log, and records it, until the test takes it
// away.
func TestAStartAndStopThatFailToPushAreWrittenLater(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a := newTimedDevice(t, remote, 300*time.Millisecond, 0)
	dev := a.self()
	mesh := "refs/murmur/mesh/" + dev
	eventually(t, "the device's presence", func() bool {
		return git(t, remote, "for-each-ref", "refs/murmur/presence/"+dev) != ""
	})
	hook, refused := filepath.Join(remote, "hooks", "pre-receive"), filepath.Join(remote, "refused")
	script := "#!/bin/sh\n" +
		"while read old new ref; do case \"$ref\" in refs/murmur/mesh/*) echo \"$ref\" >> \"$GIT_DIR/refused\"; exit 1;; esac; done\n" +
		"exit 0\n"
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	addr, conn := a.session("aaaaaa", "claude-a")
	eventually(t, "the session's start to be refused", func() bool {
		_, err := os.Stat(refused)
		return err == nil
	})
	conn.Close()
	eventually(t, "the session gone from the device's presence, its grace over", func() bool {
		return !strings.Contains(openedPresence(t, remote, a), `"aaaaaa"`)
	})
	if git(t, remote, "for-each-ref", mesh) != "" {
		t.Fatalf("the log was pushed past the hook:\n%s", busText(t, remote, a))
	}

	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	var text string
	eventually(t, "the session's start and stop on its log", func() bool {
		if git(t, remote, "for-each-ref", mesh) == "" {
			return false
		}
		text = busText(t, remote, a)
		return strings.Contains(text, addr+" started (") && strings.Contains(text, addr+" stopped")
	})
	if n := strings.Count(text, addr+" started ("); n != 1 {
		t.Errorf("the start is on the log %d times:\n%s", n, text)
	}
	// Newest first.
	if strings.Index(text, addr+" stopped") > strings.Index(text, addr+" started (") {
		t.Errorf("the stop landed before the start:\n%s", text)
	}
}
