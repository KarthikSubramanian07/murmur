package daemon

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
)

// pushPresence puts a presence commit with message msg on the peer's presence ref. The message goes
// on stdin, as one this long can be too long for a command line.
func (r *giveUpRig) pushPresence(msg string) {
	r.t.Helper()
	tree := r.git(r.author, "hash-object", "-t", "tree", "-w", "--stdin")
	cmd := exec.Command("git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit-tree", tree, "-F", "-")
	cmd.Dir, cmd.Stdin = r.author, strings.NewReader(msg)
	out, err := cmd.Output()
	if err != nil {
		r.t.Fatalf("commit-tree: %v", err)
	}
	r.git(r.author, "push", "-q", "--force", r.remote, strings.TrimSpace(string(out))+":"+brand.PresencePrefix+giveUpPeer)
}

// A presence that lists more sessions than one device runs isn't Murmur's. Each name in a presence
// counts as used for good, so it isn't cut to the cap but taken for one that can't be read: its
// device is alive, and none of its names is believed. One at the cap is read.
func TestAPresenceWithTooManySessionsIsUnreadable(t *testing.T) {
	for _, c := range []struct {
		sessions int
		read     bool
	}{{bus.MaxSessions + 1, false}, {bus.MaxSessions, true}} {
		t.Run(fmt.Sprint(c.sessions), func(t *testing.T) {
			r := newGiveUpRig(t)
			listed := freshNames(t, 17, c.sessions)
			p := bus.Presence{V: bus.Version, Device: giveUpPeer, Host: "far", At: time.Now().UTC()}
			for i, name := range listed {
				p.Sessions = append(p.Sessions, bus.SessionInfo{ID: fmt.Sprintf("s%05d", i), Name: name, Agent: "claude",
					Started: time.Now().UTC()})
			}
			msg, err := bus.Encode(p, "presence")
			if err != nil {
				t.Fatal(err)
			}
			r.pushPresence(msg)
			r.sync()

			r.d.mu.Lock()
			defer r.d.mu.Unlock()
			peer := r.d.peers[giveUpPeer]
			if peer == nil {
				t.Fatal("the device isn't known")
			}
			if got := len(peer.pres.Sessions); c.read && got != c.sessions || !c.read && got != 0 {
				t.Errorf("%d sessions read from the presence", got)
			}
			if _, ok := r.d.st.Used[listed[0]]; ok != c.read {
				t.Errorf("a name in the presence taken for used: %v, want %v", ok, c.read)
			}
		})
	}
}
