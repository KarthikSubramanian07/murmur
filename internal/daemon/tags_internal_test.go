package daemon

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/t3db"
	"github.com/a-Fig/murmur/internal/tags"
)

// Session tags (#198), as the session's own device decides them.

func tagsDaemon(t *testing.T) *Daemon {
	d := threadsDaemon(t)
	d.opt.StaleAfter = 6 * time.Minute
	d.st.Tags = map[string]*tags.State{}
	d.st.Inbox = map[string][]*inMsg{}
	d.st.Stopped = map[string]time.Time{}
	d.started = time.Now().Add(-time.Hour) // past the catch-up window, so delivery isn't held
	for _, id := range []string{"aaaaaa", "bbbbbb"} {
		inT3(d, id, time.Minute)
		d.sessions[id].key = "t3:thread-" + id
	}
	return d
}

func tagNames(ts []tags.Tag) string { return strings.Join(tags.Names(ts), " ") }

func TestOwnTagsAndTheDerivedModel(t *testing.T) {
	d := tagsDaemon(t)
	d.threads[threadKey{"", "thread-aaaaaa"}] = t3db.State{Model: "claude-opus-5-5"}

	resp := d.setOwnTags("aaaaaa", proto.Request{Add: []string{"Reviewing"}})
	if resp.Error != "" {
		t.Fatal(resp.Error)
	}
	if got := tagNames(resp.Tags); got != "model:opus-5.5 reviewing" {
		t.Fatalf("tags after setting = %q", got)
	}
	if src := resp.Tags[0].Src; src != tags.SrcDerived {
		t.Errorf("the model's source is %q, want derived", src)
	}
	if resp := d.setOwnTags("aaaaaa", proto.Request{Add: []string{"no spaces"}}); resp.Error == "" {
		t.Error("a tag with a space was taken")
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	// The session's own model stands over T3's.
	d.st.Tags["t3:thread-aaaaaa"].SetSelf([]string{"model:sonnet-5.5"}, nil)
	if got := tagNames(d.tagsOfLocked(d.sessions["aaaaaa"])); got != "model:sonnet-5.5 reviewing" {
		t.Errorf("tags = %q, want the session's model in place of T3's", got)
	}

	// who lists by tags.
	f, _ := tags.ParseFilter([]string{"reviewing"})
	got := d.whoLocked("bbbbbb", f)[0].Sessions
	if len(got) != 1 || got[0].Name != "name-aaaaaa" {
		t.Errorf("who tagged reviewing = %+v", got)
	}
	f, _ = tags.ParseFilter([]string{"-reviewing"})
	if got := d.whoLocked("bbbbbb", f)[0].Sessions; len(got) != 1 || got[0].Name != "name-bbbbbb" {
		t.Errorf("who without reviewing = %+v", got)
	}
}

func TestUnavailableIsSkippedByBroadcastsButStillReads(t *testing.T) {
	d := tagsDaemon(t)
	if resp := d.setOwnTags("aaaaaa", proto.Request{Add: []string{"unavailable"}}); resp.Error != "" {
		t.Fatal(resp.Error)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.unavailableLocked("dev", "aaaaaa") || d.unavailableLocked("dev", "bbbbbb") {
		t.Fatal("unavailableLocked is wrong")
	}
	now := time.Now()
	d.deliverLocked(bus.Event{ID: "m1", Type: bus.Message, From: "other/xxxxxx", To: "*", At: now}, modeNormal)
	d.deliverLocked(bus.Event{ID: "m2", Type: bus.Message, From: "other/xxxxxx", To: "dev", At: now}, modeNormal)
	if len(d.st.Inbox["aaaaaa"]) != 0 || len(d.st.Inbox["bbbbbb"]) != 2 {
		t.Fatalf("after two broadcasts: unavailable has %d, available %d", len(d.st.Inbox["aaaaaa"]), len(d.st.Inbox["bbbbbb"]))
	}
	// A message sent to it all the same (by a daemon from before tags) is still delivered.
	d.deliverLocked(bus.Event{ID: "m3", Type: bus.Message, From: "other/xxxxxx", To: "dev/aaaaaa", At: now}, modeNormal)
	if len(d.st.Inbox["aaaaaa"]) != 1 {
		t.Fatal("a direct message to an unavailable session was dropped")
	}

	// Another device's session, by its presence; not when that presence has gone stale.
	d.peers["far"] = &peer{lastChange: time.Now(), pres: bus.Presence{Sessions: []bus.SessionInfo{
		{ID: "cccccc", Tags: []tags.Tag{{Name: tags.Unavailable, Src: tags.SrcHuman}}}}}}
	d.lastPollOK = time.Now()
	if !d.unavailableLocked("far", "cccccc") {
		t.Error("a peer's unavailable session isn't seen as such")
	}
	d.peers["far"].lastChange = time.Now().Add(-time.Hour)
	if d.unavailableLocked("far", "cccccc") {
		t.Error("a stale peer's session is taken for unavailable")
	}
}

func TestAHumansTagsStandOverTheSessions(t *testing.T) {
	d := tagsDaemon(t)
	d.setOwnTags("aaaaaa", proto.Request{Add: []string{"reviewing"}})
	d.mu.Lock()
	d.peers["far"] = &peer{pres: bus.Presence{Owner: "Maya"}}
	// From another device's page: unavailable on, reviewing off.
	e := bus.Event{V: bus.Version, ID: "abcdef12", Type: bus.Tags, From: "far/cli", To: "dev/aaaaaa", At: time.Now(),
		Add: []string{"unavailable"}, Remove: []string{"reviewing"}}
	if !e.Valid() {
		t.Fatal("the tags event isn't valid")
	}
	d.applyTagsEventLocked(e)
	// One for a session elsewhere does nothing here.
	d.applyTagsEventLocked(bus.Event{Type: bus.Tags, From: "far/cli", To: "other/aaaaaa", Add: []string{"x"}})
	got := d.tagsOfLocked(d.sessions["aaaaaa"])
	d.mu.Unlock()
	if want := []tags.Tag{{Name: "unavailable", Src: tags.SrcHuman}}; !slices.Equal(got, want) {
		t.Fatalf("tags = %+v, want %+v", got, want)
	}
	if m := d.st.Tags["t3:thread-aaaaaa"].Human["unavailable"]; m.By != "Maya on far" {
		t.Errorf("the mark is by %q", m.By)
	}

	// The session can't undo the human's choice, and is told so.
	resp := d.setOwnTags("aaaaaa", proto.Request{Add: []string{"reviewing"}, Remove: []string{"unavailable"}})
	if !strings.Contains(resp.Note, "a human") || tagNames(resp.Tags) != "unavailable" {
		t.Errorf("after the session's change: %q, %q", resp.Note, tagNames(resp.Tags))
	}

	// The tags are kept by the session's key, so a new ID for the same thread has them.
	d.mu.Lock()
	defer d.mu.Unlock()
	inT3(d, "dddddd", 0)
	d.sessions["dddddd"].key = "t3:thread-aaaaaa"
	if !tags.Has(d.tagsOfLocked(d.sessions["dddddd"]), tags.Unavailable) {
		t.Error("the thread's tags didn't follow it to its new session ID")
	}
}

func TestTagsEventValid(t *testing.T) {
	base := bus.Event{V: bus.Version, ID: "abcdef12", Type: bus.Tags, From: "far/cli", To: "dev/aaaaaa", Add: []string{"ok"}}
	if !base.Valid() {
		t.Fatal("a good tags event isn't valid")
	}
	for name, mod := range map[string]func(*bus.Event){
		"to a device":      func(e *bus.Event) { e.To = "dev" },
		"to everyone":      func(e *bus.Event) { e.To = "*" },
		"nothing changed":  func(e *bus.Event) { e.Add = nil },
		"a bad tag":        func(e *bus.Event) { e.Add = []string{"Not A Tag"} },
		"too many at once": func(e *bus.Event) { e.Add = slices.Repeat([]string{"x"}, tags.MaxPerSession+1) },
	} {
		e := base
		mod(&e)
		if e.Valid() {
			t.Errorf("%s: valid", name)
		}
	}
}

func TestAChangeForASessionThatIsntRegisteredWaitsForIt(t *testing.T) {
	d := tagsDaemon(t)
	d.st.NameKeys = map[string]string{"t3:thread-aaaaaa": "name-aaaaaa"}
	d.st.SessionNames["aaaaaa"] = "name-aaaaaa"
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.sessions, "aaaaaa") // its daemon has restarted, and it hasn't registered again yet
	d.applyTagsEventLocked(bus.Event{V: bus.Version, ID: "abcdef12", Type: bus.Tags, From: "far/cli",
		To: "dev/aaaaaa", At: time.Now(), Add: []string{"unavailable"}})
	inT3(d, "aaaaaa", 0)
	d.sessions["aaaaaa"].key = "t3:thread-aaaaaa"
	if !d.unavailableLocked("dev", "aaaaaa") {
		t.Fatal("the change made while the session was away was lost")
	}
}

func TestCatchUpSkipsAnUnavailableSession(t *testing.T) {
	d := tagsDaemon(t)
	d.setOwnTags("aaaaaa", proto.Request{Add: []string{"unavailable"}})
	d.mu.Lock()
	defer d.mu.Unlock()
	d.started = time.Now()
	d.caughtUp = map[string]bool{}
	now := time.Now()
	d.pending = []bus.Event{
		{ID: "m1", Type: bus.Message, From: "other/xxxxxx", To: "*", At: now},
		{ID: "m2", Type: bus.Message, From: "other/xxxxxx", To: "dev/aaaaaa", At: now},
	}
	d.catchUpLocked("aaaaaa")
	if got := d.st.Inbox["aaaaaa"]; len(got) != 1 || got[0].Event.ID != "m2" {
		t.Fatalf("caught up %d messages; want only the one sent to it", len(got))
	}
}
