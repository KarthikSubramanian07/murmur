package daemon

import (
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/tags"
)

// A session on another device that says when it started but not when it was last active is ranked in
// `who` by its start. One dated a day ahead would sit above every other session, on every device, for
// a day, and push real ones into "and N more"; it is shown as started no later than its presence.
func TestAPeerSessionStartedInTheFutureIsShownAsNoLater(t *testing.T) {
	r := newGiveUpRig(t)
	now := time.Now().UTC()
	p := bus.Presence{V: bus.Version, Device: giveUpPeer, Host: "far", At: now,
		Sessions: []bus.SessionInfo{{ID: "aaaaaa", Agent: "claude", Started: now.Add(24 * time.Hour)}}}
	msg, err := bus.Encode(p, "presence")
	if err != nil {
		t.Fatal(err)
	}
	tree := r.git(r.author, "hash-object", "-t", "tree", "-w", "--stdin")
	sha := r.git(r.author, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit-tree", tree, "-m", msg)
	r.git(r.author, "push", "-q", "--force", r.remote, sha+":"+brand.PresencePrefix+giveUpPeer)
	r.sync()

	r.d.mu.Lock()
	devices := r.d.whoLocked("", tags.Filter{})
	r.d.mu.Unlock()
	for _, dv := range devices {
		if dv.ID != giveUpPeer {
			continue
		}
		if len(dv.Sessions) != 1 {
			t.Fatalf("the device lists %d sessions, want 1", len(dv.Sessions))
		}
		if started := dv.Sessions[0].Started; started.After(time.Now()) {
			t.Errorf("the session is shown as started at %v, after now", started)
		}
		return
	}
	t.Fatal("the device isn't listed")
}
