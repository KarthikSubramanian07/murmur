package daemon

import (
	"slices"
	"strings"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
)

// A message handed over as a turn is read when that turn starts (#611). Murmur hands it to the
// client (T3, Claude Code's socket, a client's command line) and can't see the turn start, so it is
// marked handed over. Where the hook runs, the turn's start fires UserPromptSubmit, whose prompt is
// the turn: once a prompt holds a message handed to the session, the message is read. A prompt the
// user typed while the client held Murmur's turn in its queue doesn't hold it, so it doesn't count.
// Only messages that asked for receipts are remembered, as nothing else has a mark to move.

// awaitingFor is how long a message handed over as a turn waits for a prompt that holds it before
// it is forgotten, and stays handed over: a turn T3 queued behind a long one can wait hours.
const awaitingFor = 24 * time.Hour

// maxAwaiting caps the messages one session waits on so, the newest kept.
const maxAwaiting = 20

// awaitingRead is a message handed to a session as a turn and not yet seen in a prompt: what
// markLocked needs of it, and what a prompt is matched by.
type awaitingRead struct {
	ID      string    `json:"id"`
	From    string    `json:"from"`
	Subject string    `json:"subject"`
	At      time.Time `json:"at"`
}

// awaitReadLocked remembers that message e was handed to session id as a turn, if it asked for
// receipts. The caller saves the state. Caller holds mu.
func (d *Daemon) awaitReadLocked(id string, e bus.Event) {
	if !e.Receipts || e.Type != bus.Message || strings.TrimSpace(e.Subject) == "" {
		return
	}
	if d.st.AwaitingRead == nil {
		d.st.AwaitingRead = map[string][]awaitingRead{}
	}
	list := slices.DeleteFunc(d.st.AwaitingRead[id], func(a awaitingRead) bool { return time.Since(a.At) > awaitingFor })
	list = append(list, awaitingRead{ID: e.ID, From: e.From, Subject: e.Subject, At: time.Now()})
	if over := len(list) - maxAwaiting; over > 0 {
		list = list[over:]
	}
	d.st.AwaitingRead[id] = list
}

// readOnPromptLocked marks read each message handed to session id as a turn that prompt holds, and
// forgets it. It says whether it marked any; the caller saves the state then. Caller holds mu.
func (d *Daemon) readOnPromptLocked(id, prompt string) bool {
	list := d.st.AwaitingRead[id]
	if prompt == "" || len(list) == 0 {
		return false
	}
	marked := false
	keep := list[:0]
	for _, a := range list {
		if !holds(prompt, a.Subject) {
			if time.Since(a.At) <= awaitingFor {
				keep = append(keep, a)
			}
			continue
		}
		d.markLocked(id, bus.Event{Type: bus.Message, ID: a.ID, From: a.From, Receipts: true}, bus.MarkRead)
		marked = true
	}
	if len(keep) == 0 {
		delete(d.st.AwaitingRead, id)
	} else {
		d.st.AwaitingRead[id] = keep
	}
	return marked
}

// holds says prompt is the turn of a message with this subject. The turn prints the subject as a
// line of its own, or, where the subject is only the start of the body cut short with "…" or "..."
// (view.repeatsSubject), the body, which begins with that start.
func holds(prompt, subject string) bool {
	stem := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(subject, "…"), "..."))
	return stem != "" && strings.Contains(prompt, stem)
}
