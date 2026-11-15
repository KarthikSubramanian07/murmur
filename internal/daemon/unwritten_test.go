package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/web"
)

// A session's start that fails to reach the remote is written once it can be (#434), in the same
// run or the next: the Recent view on another device shows it, and nothing else would put it on
// the bus.

// unwritten is what d's state file holds back to be written to its log.
func (d *device) unwritten() []bus.Event {
	d.t.Helper()
	var st struct {
		Unwritten []bus.Event `json:"unwritten"`
	}
	b, err := os.ReadFile(filepath.Join(d.gitDir, brand.StateDir, "state.json"))
	if err != nil || json.Unmarshal(b, &st) != nil {
		return nil // being replaced: asked again
	}
	return st.Unwritten
}

// startFailed starts session id on d while d can't push, and waits until its start has failed to
// be written.
func startFailed(t *testing.T, d *device, remote, id string) string {
	t.Helper()
	d.cantPush(remote, true)
	addr, _ := d.session(id, "")
	eventually(t, "the start's write to fail", func() bool {
		return strings.Contains(d.tail.String(), "writing "+bus.SessionStarted)
	})
	if got := d.unwritten(); len(got) != 1 || got[0].Type != bus.SessionStarted || got[0].From != addr {
		t.Fatalf("held back to be written: %+v, want the start of %s", got, addr)
	}
	return addr
}

// startShown waits for the start of the session at addr on d's Recent view, and for the device it
// ran on to have nothing left to write.
func startShown(t *testing.T, d, on *device, addr string) {
	t.Helper()
	pg := d.page()
	eventually(t, "the start on the other device's page", func() bool {
		acts, _ := pg.activity("")
		return hasAct(acts, web.ActStarted, addr, nil)
	})
	eventually(t, "nothing left to write", func() bool { return len(on.unwritten()) == 0 })
}

func TestAStartThatFailedToPushIsWrittenOnceItCanBe(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	bob, _ := b.session("bbbbbb", "")
	eventually(t, "b's presence on a", func() bool { return a.seesSession(bob) })

	alice := startFailed(t, a, remote, "aaaaaa")
	a.cantPush(remote, false)
	startShown(t, b, a, alice)
}

func TestAStartThatFailedToPushIsWrittenByTheNextRun(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	bob, _ := b.session("bbbbbb", "")
	eventually(t, "b's presence on a", func() bool { return a.seesSession(bob) })

	alice := startFailed(t, a, remote, "aaaaaa")
	a.stop()
	a.cantPush(remote, false)
	a.start()
	startShown(t, b, a, alice)
}
