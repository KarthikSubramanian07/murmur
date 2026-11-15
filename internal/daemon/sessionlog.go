package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
)

// A session's start and stop on this device's log (#434). Nobody waits on them, so they are
// written in the background, but the page's Recent view shows them ("joined", "left"), from the
// bus as each device reads it and from the history a daemon reads back as it starts, so one that
// fails to push is kept in the state (Owed) and tried again, as a receipt is: after markRetry,
// doubling to markRetryMax, whenever a presence is pushed (the remote is taking pushes again), or
// by the next daemon. They go out one at a time, oldest first, so a session's stop never lands
// before its start.

// maxOwed caps the starts and stops waiting to be written; past it the oldest go. A device whose
// remote is down for days would otherwise keep every one. 100 is a busy day's sessions, each
// started and stopped, and keeps the state small (about 25 KB): what is dropped is the oldest
// news, which the page's Recent view is least likely to still show.
const maxOwed = 100

// owedEvent is a start or stop not yet known to be on the log. Tried is the commit a write of it
// last pushed and was told failed: a push can land and still report a failure (bus.Push looks,
// but can't always tell), so the next write checks whether the log holds it before writing it
// again.
type owedEvent struct {
	Event bus.Event `json:"event"`
	Tried string    `json:"tried,omitempty"`
}

// owe keeps e, a session's start or stop, to be written to this device's log by owedLoop.
func (d *Daemon) owe(e bus.Event) {
	d.mu.Lock()
	d.st.Owed = append(d.st.Owed, owedEvent{Event: e})
	if n := len(d.st.Owed) - maxOwed; n > 0 {
		for _, o := range d.st.Owed[:n] {
			d.log.Printf("dropping %s: more than %d starts and stops are waiting to be written", logged(o.Event), maxOwed)
		}
		d.st.Owed = d.st.Owed[n:]
	}
	d.saveLocked()
	d.mu.Unlock()
	d.kickOwed()
}

func (d *Daemon) kickOwed() {
	select {
	case d.owedKick <- struct{}{}:
	default:
	}
}

// owedLoop writes the starts and stops waiting in the state, the ones the daemon before this one
// left included.
func (d *Daemon) owedLoop(ctx context.Context) {
	d.kickOwed() // what the daemon before this one didn't write
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	defer retry.Stop()
	wait := markRetry
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.owedKick:
		case <-retry.C:
		}
		err := d.writeOwed(ctx)
		if err == nil {
			retry.Stop()
			wait = markRetry
			continue
		}
		if ctx.Err() != nil {
			return
		}
		d.log.Printf("%v; trying again in %s", err, wait)
		retry.Reset(wait)
		wait = min(2*wait, markRetryMax)
	}
}

// writeOwed writes the starts and stops waiting, oldest first, until one fails. Each goes after
// the presence of a session just started (see awaitPresence), and takes a minute at most.
func (d *Daemon) writeOwed(ctx context.Context) error {
	for {
		d.mu.Lock()
		if len(d.st.Owed) == 0 {
			d.mu.Unlock()
			return nil
		}
		o := d.st.Owed[0]
		d.mu.Unlock()
		wctx, cancel := context.WithTimeout(ctx, time.Minute)
		err := d.awaitPresence(wctx)
		pushed := ""
		if err == nil {
			pushed, err = d.writeEvent(wctx, o.Event, o.Tried)
		}
		cancel()
		d.mu.Lock()
		if len(d.st.Owed) > 0 && d.st.Owed[0].Event.ID == o.Event.ID { // not dropped meanwhile
			if err == nil {
				d.st.Owed = d.st.Owed[1:]
				d.saveLocked()
			} else if pushed != "" {
				d.st.Owed[0].Tried = pushed
				d.saveLocked()
			}
		}
		d.mu.Unlock()
		if err != nil {
			return fmt.Errorf("writing %s: %w", logged(o.Event), err)
		}
	}
}
