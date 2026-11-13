package daemon

import (
	"io"
	"log"
	"slices"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/web"
)

func historyDaemon(pageMessages int) *Daemon {
	return &Daemon{opt: Options{PageMessages: pageMessages}, log: log.New(io.Discard, "", 0), device: "here-0001",
		started: time.Now(), msgIDs: map[string]bool{}, actIDs: map[string]bool{}}
}

var historyBase = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

// sentAt is a message with id as its ID and subject, sent min minutes into the test's morning.
func sentAt(id string, min int) bus.Event {
	return bus.Event{V: bus.Version, ID: id, Type: bus.Message, From: "there-0002/aaaaaa", To: "*", Subject: id,
		At: historyBase.Add(time.Duration(min) * time.Minute)}
}

func (d *Daemon) pageSubjects() (subjects []string, seqs []uint64) {
	for _, m := range d.msgs {
		subjects, seqs = append(subjects, m.e.Subject), append(seqs, m.seq)
	}
	return subjects, seqs
}

// What is read back goes among the page's messages by when it was sent, each once, and the page's
// limit keeps the newest. A page that asked before is given them all again, as their numbers are
// new; it knows the ones it has by their IDs.
func TestHistoryMergesByTimeAndRenumbers(t *testing.T) {
	d := historyDaemon(4)
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, e := range []bus.Event{sentAt("m3", 3), sentAt("m5", 5)} {
		d.keepMessageLocked(e)
	}
	before := d.msgSeq

	d.mergeMessagesLocked([]bus.Event{sentAt("m1", 1), sentAt("m2", 2), sentAt("m3", 3), sentAt("m4", 4), sentAt("m4", 4)})
	got, seqs := d.pageSubjects()
	if !slices.Equal(got, []string{"m2", "m3", "m4", "m5"}) {
		t.Fatalf("the page holds %q, want the newest four in the order sent", got)
	}
	for i, seq := range seqs {
		if seq <= before || i > 0 && seq <= seqs[i-1] {
			t.Fatalf("numbers %v: each must pass %d and the one before, or a cursor from before misses it", seqs, before)
		}
	}
	if len(d.msgIDs) != 4 || d.msgIDs["m1"] || !d.msgIDs["m4"] {
		t.Errorf("the IDs known are %v", d.msgIDs)
	}
}

// With nothing new to keep, the page's messages are left as they were, numbers and all: a page isn't
// given again what it has each time the logs are read back. That holds when what was read is all
// older than a full page's messages, as when a message was dropped while the read was under way.
func TestHistoryWithNothingToAddChangesNothing(t *testing.T) {
	d := historyDaemon(3)
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, e := range []bus.Event{sentAt("m3", 3), sentAt("m4", 4), sentAt("m5", 5)} {
		d.keepMessageLocked(e)
	}
	_, was := d.pageSubjects()
	for _, read := range [][]bus.Event{
		nil,
		{sentAt("m3", 3), sentAt("m5", 5)},
		{sentAt("m1", 1), sentAt("m2", 2), sentAt("m4", 4)},
	} {
		d.mergeMessagesLocked(read)
		got, seqs := d.pageSubjects()
		if !slices.Equal(got, []string{"m3", "m4", "m5"}) || !slices.Equal(seqs, was) || d.msgSeq != was[2] {
			t.Fatalf("after reading back %d: %q numbered %v up to %d, want them as they were, %v", len(read), got, seqs, d.msgSeq, was)
		}
		if len(d.msgIDs) != 3 || d.msgIDs["m1"] {
			t.Fatalf("the IDs known are %v", d.msgIDs)
		}
	}
}

// A person's own messages are counted apart from the rest when history is merged, as when they
// are read.
func TestHistoryCountsAPersonsMessagesApart(t *testing.T) {
	d := historyDaemon(2)
	d.mu.Lock()
	defer d.mu.Unlock()
	own := sentAt("mine", 1)
	own.To = d.personAddr()
	d.mergeMessagesLocked([]bus.Event{own, sentAt("m2", 2), sentAt("m3", 3), sentAt("m4", 4)})
	if got, _ := d.pageSubjects(); !slices.Equal(got, []string{"mine", "m3", "m4"}) || d.ownMsgs != 1 {
		t.Errorf("the page holds %q with %d of the person's, want theirs and the newest two", got, d.ownMsgs)
	}
}

// The Recent view's activity merges the same way.
func TestHistoryMergesActivity(t *testing.T) {
	d := historyDaemon(3)
	d.mu.Lock()
	defer d.mu.Unlock()
	act := func(id string, min int) web.Activity {
		return web.Activity{ID: id, Kind: web.ActStarted, Addr: "there-0002/aaaaaa", At: historyBase.Add(time.Duration(min) * time.Minute)}
	}
	d.recordActLocked(act("a2", 2))
	d.recordActLocked(web.Activity{Kind: web.ActStatus, Addr: "there-0002/aaaaaa", Doing: "now", At: historyBase.Add(4 * time.Minute)})
	before := d.actSeq

	d.mergeActsLocked([]web.Activity{act("a2", 2)})
	if d.actSeq != before || len(d.acts) != 2 {
		t.Fatalf("reading back what it has changed the activity: %d kept, numbered to %d", len(d.acts), d.actSeq)
	}
	d.mergeActsLocked([]web.Activity{act("a1", 1), act("a3", 3), act("a2", 2)})
	var kinds []string
	for i, p := range d.acts {
		kinds = append(kinds, p.a.Kind+" "+p.a.At.Sub(historyBase).String())
		if p.seq <= before || i > 0 && p.seq <= d.acts[i-1].seq {
			t.Fatalf("activity %d is numbered %d, after %d", i, p.seq, before)
		}
	}
	if !slices.Equal(kinds, []string{web.ActStarted + " 1m0s", web.ActStarted + " 2m0s", web.ActStarted + " 3m0s", web.ActStatus + " 4m0s"}) {
		t.Errorf("the activity is %q, want the three starts and then the status", kinds)
	}
}
