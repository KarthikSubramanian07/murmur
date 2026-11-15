package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/proto"
)

// A T3 thread idle long enough has its process stopped by T3, and its session with it, but a
// message still wakes it through its route. Its sender is told that, not that it has stopped and
// may not come back, which read as the thread being gone (#669). A session with no route has.
func TestASenderIsToldAnIdleT3ThreadWakes(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	exe, calls := useFakeT3(t)
	a := newDeviceWith(t, remote, func(d *device) { d.grace = 300 * time.Millisecond })
	a.session("aaaaaa", "")
	bob, conn := a.sessionRoute("bbbbbb", "", proto.Route{T3CLI: exe, T3Thread: "thread-bob"})
	carol, carolConn := a.session("cccccc", "")
	conn.Close()
	carolConn.Close()
	eventually(t, "bob and carol to stop", func() bool {
		return busLines(a, bob, "stopped") == 1 && busLines(a, carol, "stopped") == 1
	})

	r := a.send("aaaaaa", bob, "are you there?", "")
	if r.Error != "" {
		t.Fatal(r.Error)
	}
	if !strings.Contains(r.Note, "idle in T3") || strings.Contains(r.Note, "has stopped") {
		t.Errorf("the note for an idle T3 thread is %q, want that it is idle in T3 and is woken", r.Note)
	}
	eventually(t, "the handoff to the sleeping thread", func() bool { return len(calls()) == 1 })

	if r := a.send("aaaaaa", carol, "are you there?", ""); !strings.Contains(r.Note, "has stopped") {
		t.Errorf("the note for a stopped session with no route is %q, want that it has stopped", r.Note)
	}
}
