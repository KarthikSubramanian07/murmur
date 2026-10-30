package daemon_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/t4/internal/proto"
)

// `t3 thread send` takes no key that would make a repeat harmless, so a delivery that failed after
// `t3` started may have put the message in the thread, and trying again would make a second turn of
// it. Only an attempt that cannot have reached T3 is made twice (#63).

// lateT3 is a `t3` that isn't there yet: exe names a command that doesn't exist, until install puts
// the stand-in in its place. A delivery to it fails before anything starts.
func lateT3(t *testing.T) (exe string, install func(), calls func() []t3Call) {
	t.Helper()
	stand, calls := useFakeT3(t)
	exe = filepath.Join(t.TempDir(), "t3"+filepath.Ext(stand))
	install = func() {
		src, err := os.Open(stand)
		if err != nil {
			t.Fatal(err)
		}
		defer src.Close()
		tmp := exe + ".tmp"
		dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(dst, src)
		if cerr := dst.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, exe); err != nil { // whole, or not there: never half a command
			t.Fatal(err)
		}
	}
	return exe, install, calls
}

// A `t3` that took the message and then failed, as one does that times out or has its output lost
// after T3 accepted it, is not run again: the message may be in the thread already, and it would
// be there twice. It waits for the hook's notice instead, and is never delivered a second time.
func TestADeliveryThatMayHaveLandedIsNotRepeated(t *testing.T) {
	remote := newRemote(t)
	exe, calls := useFakeT3(t)
	t.Setenv(fakeT3FailEnv, "1") // the stand-in records the call, which is T3 having it, and then fails
	a, b := newDevice(t, remote), newDeviceWith(t, remote, func(d *device) { d.pushRetry = 300 * time.Millisecond })
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.sessionRoute("bbbbbb", "claude-bob", proto.Route{T3CLI: exe, T3Thread: "thread-bob"})
	eventually(t, "presence", func() bool { return a.seesSession(bob) && b.seesSession(alice) })

	a.send("aaaaaa", bob, "might have landed", "body")
	eventually(t, "the attempt", func() bool { return len(calls()) == 1 })
	holds(t, "no second call after the retry wait", 2*time.Second, func() bool { return len(calls()) == 1 })
	var notices []string
	eventually(t, "the notice", func() bool {
		notices = append(notices, b.hook("claude-bob", 0)...)
		return len(notices) > 0
	})
	if notices[0] != "might have landed" {
		t.Errorf("notices: %q", notices)
	}
}

// An attempt that never started anything is the one kind that is tried again, once: no `t3` is
// there yet when the first attempt is made, and the retry finds it.
func TestADeliveryThatNeverStartedIsTriedOnceMore(t *testing.T) {
	remote := newRemote(t)
	exe, install, calls := lateT3(t)
	var out syncBuf
	a := newDevice(t, remote)
	b := newDeviceWith(t, remote, func(d *device) { d.logTo, d.pushRetry = &out, 2*time.Second })
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.sessionRoute("bbbbbb", "claude-bob", proto.Route{T3CLI: exe, T3Thread: "thread-bob"})
	eventually(t, "presence", func() bool { return a.seesSession(bob) && b.seesSession(alice) })

	a.send("aaaaaa", bob, "second try", "body")
	eventually(t, "the first attempt to fail before it started", func() bool {
		return strings.Contains(out.String(), "nothing was sent")
	})
	install()
	eventually(t, "the retry", func() bool { return len(calls()) == 1 })
	eventually(t, "the message to be read", func() bool {
		inbox, notices := b.unread("bbbbbb", "claude-bob")
		return len(inbox) == 0 && len(notices) == 0
	})
	holds(t, "one call and no failure logged", time.Second, func() bool {
		return len(calls()) == 1 && !strings.Contains(out.String(), "as a turn through T3 failed")
	})
}

// A retry that fails to start too leaves the message to the notice, after two attempts and no more.
func TestADeliveryThatNeverStartsFallsBackToTheNotice(t *testing.T) {
	remote := newRemote(t)
	exe, _, calls := lateT3(t)
	var out syncBuf
	a := newDevice(t, remote)
	b := newDeviceWith(t, remote, func(d *device) { d.logTo, d.pushRetry = &out, 300*time.Millisecond })
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.sessionRoute("bbbbbb", "claude-bob", proto.Route{T3CLI: exe, T3Thread: "thread-bob"})
	eventually(t, "presence", func() bool { return a.seesSession(bob) && b.seesSession(alice) })

	a.send("aaaaaa", bob, "no t3", "body")
	eventually(t, "the failure", func() bool { return strings.Contains(out.String(), "as a turn through T3 failed") })
	if n := strings.Count(out.String(), "nothing was sent"); n != 1 {
		t.Errorf("the retry was logged %d times, want once:\n%s", n, out.String())
	}
	eventually(t, "the notice", func() bool { return len(b.hook("claude-bob", 0)) > 0 })
	if n := len(calls()); n != 0 {
		t.Errorf("the stand-in ran %d times with no command there", n)
	}
}
