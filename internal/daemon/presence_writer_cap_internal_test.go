package daemon

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
)

// A reader takes a presence over bus.MaxSessions for one it can't read, so a device running more
// than that would vanish from every teammate's view. Its own presence keeps to the cap: the most
// recently active sessions are listed, and its peers read them.
func TestAPresenceKeepsToTheSessionCapItsReadersHold(t *testing.T) {
	writer, reader := newGiveUpRig(t), newGiveUpRig(t)
	writer.d.repo.Remote = reader.remote // the two share the reader's remote
	now := time.Now()
	writer.d.mu.Lock()
	// Both hold the team key the presence is encrypted for; no daemon loop runs here to make one.
	ep := withEpoch(writer.d, true)
	reader.d.mu.Lock()
	reader.d.st.Epochs, reader.d.teamKeys[ep.ID] = append(reader.d.st.Epochs, ep), writer.d.teamKeys[ep.ID]
	reader.d.mu.Unlock()
	for i := range bus.MaxSessions + 1 {
		id := fmt.Sprintf("s%05d", i)
		// s00000 is the least recently active.
		writer.d.sessions[id] = &session{id: id, agent: "claude", started: now.Add(-time.Hour),
			lastActive: now.Add(time.Duration(i-bus.MaxSessions) * time.Second)}
	}
	writer.d.mu.Unlock()
	if err := writer.d.publishPresence(context.Background()); err != nil {
		t.Fatal(err)
	}
	reader.sync()

	reader.d.mu.Lock()
	defer reader.d.mu.Unlock()
	peer := reader.d.peers[writer.d.device]
	if peer == nil {
		t.Fatal("the writer's device isn't known")
	}
	if got := len(peer.pres.Sessions); got != bus.MaxSessions {
		t.Fatalf("the reader has %d of the writer's sessions, want %d", got, bus.MaxSessions)
	}
	for _, s := range peer.pres.Sessions {
		if s.ID == "s00000" {
			t.Error("the least recently active session was listed, in place of a more active one")
		}
	}
}

// capSessions keeps the most recently active, by their start where they say nothing of activity, in
// the order they came.
func TestCapSessionsKeepsTheMostRecentlyActiveInOrder(t *testing.T) {
	now := time.Now()
	ss := []bus.SessionInfo{
		{ID: "a", LastActive: now.Add(-3 * time.Minute)},
		{ID: "b", Started: now.Add(-time.Minute)},
		{ID: "c", LastActive: now.Add(-10 * time.Minute)},
		{ID: "d", LastActive: now},
	}
	got, cut := capSessions(ss, 3)
	var ids string
	for _, s := range got {
		ids += s.ID
	}
	if ids != "abd" || cut != 1 {
		t.Errorf("kept %q, cut %d; want abd and 1", ids, cut)
	}
	if got, cut := capSessions(ss, 4); len(got) != 4 || cut != 0 {
		t.Errorf("at the cap: kept %d, cut %d", len(got), cut)
	}
}
