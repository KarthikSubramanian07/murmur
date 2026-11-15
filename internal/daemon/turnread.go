package daemon

import (
	"slices"
	"strings"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
)

// A message handed over as a turn is read when that turn starts (#611). Murmur hands it to the
// client (T3, Claude Code's socket, a client's command line) and can't see the turn start, so it is
// marked handed over. Where the hook runs, the turn's start fires UserPromptSubmit, whose prompt is
// the turn: once a prompt is the turn of a message handed to the session, the message is read. A
// prompt the user typed while the client held Murmur's turn in its queue isn't, so it doesn't
// count. Only messages that asked for receipts are remembered, as nothing else has a mark to move.

// awaitingFor is how long a message handed over as a turn waits for a prompt that holds it before
// it is forgotten, and stays handed over: a turn T3 queued behind a long one can wait hours.
const awaitingFor = 24 * time.Hour

// maxAwaiting caps the messages one session waits on so, the newest kept.
const maxAwaiting = 20

// awaitingRead is a message on its way into a session as a turn, or handed over as one, and not yet
// seen in a prompt: what markLocked needs of it, and what a prompt is matched by.
type awaitingRead struct {
	ID      string    `json:"id"`
	From    string    `json:"from"`
	Subject string    `json:"subject"`
	At      time.Time `json:"at"`
}

// awaitReadLocked remembers that message e is being handed to session id as a turn, if it asked for
// receipts. It is done as the handoff begins, as the turn can start before the client's command
// returns. Entries past awaitingFor go, for every session, so a session that has gone leaves none
// behind for long. The caller saves the state. Caller holds mu.
func (d *Daemon) awaitReadLocked(id string, e bus.Event) {
	if !e.Receipts || e.Type != bus.Message || strings.TrimSpace(e.Subject) == "" {
		return
	}
	if d.st.AwaitingRead == nil {
		d.st.AwaitingRead = map[string][]awaitingRead{}
	}
	for sess, list := range d.st.AwaitingRead {
		if list = slices.DeleteFunc(list, func(a awaitingRead) bool { return time.Since(a.At) > awaitingFor }); len(list) == 0 {
			delete(d.st.AwaitingRead, sess)
		} else {
			d.st.AwaitingRead[sess] = list
		}
	}
	list := d.st.AwaitingRead[id]
	if slices.ContainsFunc(list, func(a awaitingRead) bool { return a.ID == e.ID }) {
		return
	}
	list = append(list, awaitingRead{ID: e.ID, From: e.From, Subject: e.Subject, At: time.Now()})
	if over := len(list) - maxAwaiting; over > 0 {
		list = list[over:]
	}
	d.st.AwaitingRead[id] = list
}

// forgetAwaitingLocked drops message msg from what session id waits on, as its handoff failed.
// Caller holds mu.
func (d *Daemon) forgetAwaitingLocked(id, msg string) {
	list := slices.DeleteFunc(d.st.AwaitingRead[id], func(a awaitingRead) bool { return a.ID == msg })
	if len(list) == 0 {
		delete(d.st.AwaitingRead, id)
	} else {
		d.st.AwaitingRead[id] = list
	}
}

// readOnPromptLocked marks read the message handed to session id as a turn that prompt is, and
// forgets it. A prompt is one turn, so it marks one message at most: the oldest it matches, as the
// client takes turns in the order they were handed over. It says whether it marked one; the caller
// saves the state then. Caller holds mu.
func (d *Daemon) readOnPromptLocked(id, prompt string) bool {
	list := d.st.AwaitingRead[id]
	if prompt == "" || len(list) == 0 {
		return false
	}
	i := slices.IndexFunc(list, func(a awaitingRead) bool { return holds(prompt, a.Subject) })
	if i < 0 {
		return false
	}
	a := list[i]
	d.markLocked(id, bus.Event{Type: bus.Message, ID: a.ID, From: a.From, Receipts: true}, bus.MarkRead)
	d.forgetAwaitingLocked(id, a.ID)
	return true
}

// holds says prompt is the turn of a message with this subject, as view.Turn renders it: a header
// line ("Message from quiet-otter via Murmur:", "Message sent in group:ui from …"), then the
// subject on a line of its own, or, where the subject is only the start of the body cut short with
// "…" or "..." (view.repeatsSubject), the body, which begins with that start. Asking for Murmur's
// header keeps a short subject ("test", "done") from matching a prompt the user typed. The header
// may come after other lines, should a client put anything before the turn.
func holds(prompt, subject string) bool {
	stem := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(subject, "…"), "..."))
	if stem == "" {
		return false
	}
	lines := strings.Split(prompt, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "Message ") || !strings.Contains(line, " via "+brand.Product) {
			continue
		}
		if strings.Contains("\n"+strings.Join(lines[i+1:], "\n"), "\n"+stem) {
			return true
		}
	}
	return false
}
