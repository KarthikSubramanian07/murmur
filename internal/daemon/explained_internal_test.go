package daemon

import (
	"slices"
	"testing"

	"github.com/a-Fig/murmur/internal/bus"
)

// A turn's handoff records what the session was told against the conversation it went to (#345):
// by its name, if that lands while the handoff is under way, and never against another
// conversation the session has /cleared or /resumed into meanwhile, named or not.
func TestAHandoffRecordsAgainstItsOwnConversation(t *testing.T) {
	t.Parallel()
	d := tagsDaemon(t)
	d.st.NameKeys = map[string]string{}
	d.st.T3Routes = map[string]t3Route{}
	d.mu.Lock()
	defer d.mu.Unlock()
	ada := "dev-0001/" + bus.PersonSession
	s := d.sessions["aaaaaa"]
	job := func(id string) *pushJob {
		return &pushJob{id: id, key: d.keyOfLocked(id), conv: d.conversationLocked(id), explainKey: d.explainKeyLocked(id)}
	}
	finish := func(j *pushJob) { d.noteExplainedAtLocked(d.explainKeyOfPushLocked(j), ada) }

	// Its name lands while the handoff is under way.
	s.name = ""
	j := job(s.id)
	if j.explainKey != "conv:t3:thread-aaaaaa" {
		t.Fatalf("a pending session is kept by %q", j.explainKey)
	}
	d.st.NameKeys[s.key] = "quiet-otter"
	d.setNameLocked(s, "quiet-otter")
	finish(j)
	if !d.explainedLocked(s.id, ada) || len(d.st.Explained) != 1 {
		t.Errorf("after the name landed: %v", d.st.Explained)
	}

	// It moves to another conversation, with another name, while the handoff is under way.
	d.st.Explained = nil
	j = job(s.id)
	s.key, s.name = "t3:thread-other", "brave-heron"
	finish(j)
	if d.explainedLocked(s.id, ada) || !slices.Contains(d.st.Explained["quiet-otter"], ada) {
		t.Errorf("after a move to a named conversation: %v", d.st.Explained)
	}

	// Its name lands, and then it moves to a conversation whose name is pending, all while the
	// handoff is under way: the new conversation mustn't take what the old one was told.
	d.st.Explained = nil
	b := d.sessions["bbbbbb"]
	b.name = ""
	j = job(b.id)
	d.st.NameKeys[b.key] = "calm-finch"
	d.setNameLocked(b, "calm-finch")
	b.key, b.name = "t3:thread-next", ""
	finish(j)
	if d.explainedLocked(b.id, ada) || !slices.Contains(d.st.Explained["calm-finch"], ada) {
		t.Errorf("after the name landed and a move to a pending conversation: %v", d.st.Explained)
	}
	d.st.NameKeys[b.key] = "late-wren"
	d.setNameLocked(b, "late-wren")
	if d.explainedLocked(b.id, ada) {
		t.Errorf("the new conversation took the old one's record when its name landed: %v", d.st.Explained)
	}

	// A session of T3 that had stopped, with no name yet, comes back in the same thread while the
	// handoff is under way.
	d.st.Explained = nil
	delete(d.sessions, "cccccc")
	d.st.T3Routes["cccccc"] = t3Route{CLI: "/t3", Thread: "thread-cccccc"}
	j = job("cccccc")
	if j.explainKey != "conv:t3:thread-cccccc" {
		t.Fatalf("a stopped session of T3 is kept by %q", j.explainKey)
	}
	inT3(d, "cccccc", 0)
	c := d.sessions["cccccc"]
	c.key, c.name = "t3:thread-cccccc", ""
	d.st.NameKeys[c.key] = "bold-lark"
	d.setNameLocked(c, "bold-lark")
	finish(j)
	if !d.explainedLocked(c.id, ada) || len(d.st.Explained) != 1 {
		t.Errorf("after the session came back and its name landed: %v", d.st.Explained)
	}
}
