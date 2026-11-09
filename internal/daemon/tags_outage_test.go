package daemon_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/tags"
)

// keptTags is what state.json keeps about the tags of the session called name.
func keptTags(t *testing.T, d *device, name string) *tags.State {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(d.gitDir, brand.StateDir, "state.json"))
	if err != nil {
		return nil // being replaced; look again
	}
	var st struct {
		Tags map[string]*tags.State `json:"tags"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatalf("state.json: %v", err)
	}
	return st.Tags[name]
}

// A human takes do-not-disturb off a session while its device is away for longer than the
// catch-up limit and its inbox is kept (#230). The daemon that starts again reads the backlog in a
// skip, with no session waiting for its messages, and applies the change all the same: tags don't
// expire as messages do. Both a log this device had read before and one new to it are read.
func TestATagChangeMadeDuringALongOutageIsApplied(t *testing.T) {
	remote := newRemote(t)
	const old, fresh = "old-0001", "new-0001"
	// A log b reads before it goes away, so its backlog starts after what b has read.
	pushEvent(t, remote, "refs/murmur/mesh/"+old, forgedMessage(old, "nobody-0001/x", "earlier", time.Now()))
	b := newTimedDevice(t, remote, 300*time.Millisecond, 200*time.Millisecond)
	bdev := b.self()
	bob, conn := b.session("bbbbbb", "")
	bobName := b.named(bob)
	b.tagSelf("bbbbbb", []string{"do-not-disturb", "release"}, nil)
	conn.Close()
	eventually(t, "bob stopped", func() bool { return busLines(b, bob, "stopped") == 1 })

	b.stop()
	ageLastSync(t, b, 2*time.Hour)
	time.Sleep(300 * time.Millisecond) // bob's inbox is no longer kept
	at := time.Now().UTC()
	pushEvent(t, remote, "refs/murmur/mesh/"+old, bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Tags,
		From: old + "/cli", At: at, To: bdev + "/bbbbbb", Remove: []string{"do-not-disturb"}})
	pushEvent(t, remote, "refs/murmur/mesh/"+fresh, bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Tags,
		From: fresh + "/cli", At: at, To: bdev + "/bbbbbb", Remove: []string{"release"}})
	b.start()

	eventually(t, "the changes to be applied", func() bool {
		st := keptTags(t, b, bobName)
		if st == nil {
			return false
		}
		dnd, ok1 := st.Human["do-not-disturb"]
		rel, ok2 := st.Human["release"]
		return ok1 && !dnd.On && ok2 && !rel.On
	})
}
