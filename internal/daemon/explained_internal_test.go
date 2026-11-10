package daemon

import (
	"slices"
	"testing"

	"github.com/a-Fig/murmur/internal/bus"
)

// A turn's handoff records what the session was told against the conversation it was made for
// (#345): by its name, if that lands while the handoff is under way, and not against another
// conversation the session has /cleared or /resumed into meanwhile.
func TestAHandoffRecordsAgainstItsOwnConversation(t *testing.T) {
	t.Parallel()
	d := tagsDaemon(t)
	d.mu.Lock()
	defer d.mu.Unlock()
	ada := "dev-0001/" + bus.PersonSession
	s := d.sessions["aaaaaa"]

	// Its name lands while the handoff is under way.
	s.name = ""
	j := &pushJob{id: s.id, key: s.key, explainKey: d.explainKeyLocked(s.id)}
	if j.explainKey != "id:aaaaaa" {
		t.Fatalf("a pending session is kept by %q", j.explainKey)
	}
	s.name = "quiet-otter"
	if got := d.explainKeyOfPushLocked(j); got != "quiet-otter" {
		t.Errorf("recorded against %q, want the name that landed", got)
	}

	// It moves to another conversation, with another name, while the handoff is under way.
	j = &pushJob{id: s.id, key: s.key, explainKey: d.explainKeyLocked(s.id)}
	s.key, s.name = "t3:thread-other", "brave-heron"
	d.noteExplainedAtLocked(d.explainKeyOfPushLocked(j), ada)
	if d.explainedLocked(s.id, ada) {
		t.Error("the conversation the session moved to counts as told")
	}
	if !slices.Contains(d.st.Explained["quiet-otter"], ada) {
		t.Errorf("the conversation the turn went to isn't recorded: %v", d.st.Explained)
	}

	// What a session kept by its ID goes to its name when that lands.
	b := d.sessions["bbbbbb"]
	b.name = ""
	d.noteExplainedLocked(b.id, ada)
	b.name = "calm-finch"
	d.adoptExplainedLocked(b)
	if _, left := d.st.Explained["id:bbbbbb"]; left || !d.explainedLocked(b.id, ada) {
		t.Errorf("after the name landed: %v", d.st.Explained)
	}
}
