package daemon

import (
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/bus"
)

// A reply's room counts each message at its longest, once however many inboxes it is in, and
// refuses one that doesn't fit while taking a smaller one after it.
func TestAReplysRoomCountsEachMessageOnce(t *testing.T) {
	r := &wholeRoom{left: 3000, taken: map[string]bool{}}
	small := &inMsg{Event: bus.Event{ID: "a", Subject: "s", Body: "b"}}
	big := &inMsg{Event: bus.Event{ID: "b", Subject: "s", Body: strings.Repeat("<", 1000)}}
	if !r.take(small) || !r.take(small) {
		t.Fatal("a small message didn't fit")
	}
	if left := 3000 - (6*2 + 1<<10); r.left != left {
		t.Errorf("left %d, want %d: a message in two inboxes counted twice?", r.left, left)
	}
	if !r.has(small) || r.has(big) {
		t.Error("has doesn't say what the reply carries")
	}
	if r.take(big) {
		t.Error("a message of 6 KB escaped fit in 2 KB")
	}
	if !r.take(&inMsg{Event: bus.Event{ID: "c", Subject: "s"}}) {
		t.Error("a small message after a refused one didn't fit")
	}
	// A notice of a message the reply gives whole elsewhere is counted as well: the two are
	// separate views of it.
	before := r.left
	if r.takeNotice(small) || r.left != before {
		t.Errorf("a notice fit in %d bytes, or cost nothing (left %d)", before, r.left)
	}
}

// The room is well under what a client reads of a reply, leaving the rest of the reply its own.
func TestTheRoomForWholeMessagesFitsUnderTheClientsLimit(t *testing.T) {
	const clientMax = 64 << 20 // internal/client's maxReply
	if maxWholeReply > clientMax/2 {
		t.Errorf("whole messages may take %d of a reply the client cuts at %d", maxWholeReply, clientMax)
	}
}
