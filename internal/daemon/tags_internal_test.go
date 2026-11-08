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
	d.st.Tags["name-aaaaaa"].SetSelf([]string{"model:sonnet-5.5"}, nil)
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

func TestTagsChangeNothingAboutDelivery(t *testing.T) {
	// No tag carries behaviour: one called unavailable is a label like any other.
	d := tagsDaemon(t)
	if resp := d.setOwnTags("aaaaaa", proto.Request{Add: []string{"unavailable"}}); resp.Error != "" {
		t.Fatal(resp.Error)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	d.deliverLocked(bus.Event{ID: "m1", Type: bus.Message, From: "other/xxxxxx", To: "*", At: now}, modeNormal)
	d.deliverLocked(bus.Event{ID: "m2", Type: bus.Message, From: "other/xxxxxx", To: "dev/aaaaaa", At: now}, modeNormal)
	if len(d.st.Inbox["aaaaaa"]) != 2 || len(d.st.Inbox["bbbbbb"]) != 1 {
		t.Fatalf("inboxes: tagged %d, untagged %d", len(d.st.Inbox["aaaaaa"]), len(d.st.Inbox["bbbbbb"]))
	}
}

func TestAHumansTagsStandOverTheSessions(t *testing.T) {
	d := tagsDaemon(t)
	d.setOwnTags("aaaaaa", proto.Request{Add: []string{"reviewing"}})
	d.mu.Lock()
	d.peers["far"] = &peer{pres: bus.Presence{Owner: "Maya"}}
	// From another device's page: release on, reviewing off.
	e := bus.Event{V: bus.Version, ID: "abcdef12", Type: bus.Tags, From: "far/cli", To: "dev/aaaaaa", At: time.Now(),
		Add: []string{"release"}, Remove: []string{"reviewing"}}
	if !e.Valid() {
		t.Fatal("the tags event isn't valid")
	}
	d.applyTagsEventLocked(e)
	// One for a session elsewhere does nothing here.
	d.applyTagsEventLocked(bus.Event{Type: bus.Tags, From: "far/cli", To: "other/aaaaaa", Add: []string{"x"}})
	got := d.tagsOfLocked(d.sessions["aaaaaa"])
	d.mu.Unlock()
	if want := []tags.Tag{{Name: "release", Src: tags.SrcHuman}}; !slices.Equal(got, want) {
		t.Fatalf("tags = %+v, want %+v", got, want)
	}
	if m := d.st.Tags["name-aaaaaa"].Human["release"]; m.By != "Maya on far" {
		t.Errorf("the mark is by %q", m.By)
	}

	// The session can't undo the human's choice, and is told so.
	resp := d.setOwnTags("aaaaaa", proto.Request{Add: []string{"reviewing"}, Remove: []string{"release"}})
	if !strings.Contains(resp.Note, "a human") || tagNames(resp.Tags) != "release" {
		t.Errorf("after the session's change: %q, %q", resp.Note, tagNames(resp.Tags))
	}

	// The tags are kept by the session's name, so a new ID for the same conversation has them,
	// and so does the session once its key changes (it found its T3 thread).
	d.mu.Lock()
	defer d.mu.Unlock()
	inT3(d, "dddddd", 0)
	d.sessions["dddddd"].name = "name-aaaaaa"
	d.sessions["dddddd"].key = "claude:other"
	if !tags.Has(d.tagsOfLocked(d.sessions["dddddd"]), "release") {
		t.Error("the conversation's tags didn't follow it to its new session ID and key")
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
	// Its key was rekeyed once, so two keys have its name (see rekeyLocked); the name is what counts.
	d.st.NameKeys = map[string]string{"claude:c": "name-aaaaaa", "t3:thread-aaaaaa": "name-aaaaaa"}
	d.st.SessionNames["aaaaaa"] = "name-aaaaaa"
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.sessions, "aaaaaa") // its daemon has restarted, and it hasn't registered again yet
	d.applyTagsEventLocked(bus.Event{V: bus.Version, ID: "abcdef12", Type: bus.Tags, From: "far/cli",
		To: "dev/aaaaaa", At: time.Now(), Add: []string{"release"}})
	inT3(d, "aaaaaa", 0)
	d.sessions["aaaaaa"].key = "t3:thread-aaaaaa"
	if !tags.Has(d.tagsOfLocked(d.sessions["aaaaaa"]), "release") {
		t.Fatal("the change made while the session was away was lost")
	}
}

func TestTagsSetWhileTheNameIsPendingFollowItsName(t *testing.T) {
	d := tagsDaemon(t)
	d.st.NameHolders = map[string]string{}
	d.mu.Lock()
	s := d.sessions["aaaaaa"]
	s.name = ""
	d.mu.Unlock()
	if resp := d.setOwnTags("aaaaaa", proto.Request{Add: []string{"reviewing"}}); resp.Error != "" {
		t.Fatal(resp.Error)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.setNameLocked(s, "quiet-otter")
	if !tags.Has(d.tagsOfLocked(s), "reviewing") || d.st.Tags["id:aaaaaa"] != nil {
		t.Fatalf("after the name landed: tags %v, kept %v", d.tagsOfLocked(s), d.st.Tags)
	}
}
