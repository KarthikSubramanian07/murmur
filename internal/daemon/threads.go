package daemon

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/a-Fig/murmur/internal/t3db"
	"github.com/a-Fig/murmur/internal/web"
)

// T3 threads' own state, read from T3's database, for two things:
//   - the page's "within N minutes" wake setting (#25): a message goes into a thread as a turn only
//     if the thread is busy, or has been idle no longer than that. A thread idle that briefly still
//     has its prompt cache; a turn started after the cache has gone reads the whole conversation
//     again at full price. T3 records when each run of a thread was asked for and when it ended.
//   - settled threads (#189): a thread T3 shows as settled, archived or deleted is offline. It leaves
//     presence, so `who` and the page no longer show it, a message for it is dropped, and `send`
//     refuses one, here and, through presence's Offline list, on other devices. A thread unsettled
//     again (someone typed in it) is back with the next read.
//
// The database is read every TitleEvery, outside every lock, one query per thread, for the threads
// of the sessions here and of those stopped whose route is kept. What was last read stands while a
// read fails.

// threadKey is a T3 thread, by T3's data directory and its ID.
type threadKey struct{ home, thread string }

// threadLoop reads the threads' state at once, then every TitleEvery.
func (d *Daemon) threadLoop(ctx context.Context) {
	t := time.NewTicker(d.opt.TitleEvery)
	defer t.Stop()
	for {
		d.refreshThreads(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// refreshThreads reads the state of every thread a session here is in, or a stopped session whose
// inbox is kept was in. A change to whether a thread is settled is published, and any change lets
// the deliveries look again.
func (d *Daemon) refreshThreads(ctx context.Context) {
	d.mu.Lock()
	routes := map[threadKey]t3Route{}
	for _, s := range d.sessions {
		if r := s.route; r.T3CLI != "" && r.T3Thread != "" {
			routes[threadKey{r.T3Home, r.T3Thread}] = t3Route{CLI: r.T3CLI, Home: r.T3Home, Thread: r.T3Thread}
		}
	}
	for id, r := range d.st.T3Routes {
		if d.sessions[id] == nil && d.receivesLocked(id) {
			routes[threadKey{r.Home, r.Thread}] = r
		}
	}
	d.mu.Unlock()

	read := map[threadKey]t3db.State{}
	failed := map[threadKey]error{}
	for k, r := range routes {
		if ctx.Err() != nil {
			return
		}
		st, err := d.opt.ThreadState(ctx, r.CLI, r.Home, r.Thread)
		if err != nil {
			failed[k] = err
			continue
		}
		read[k] = st
	}

	d.mu.Lock()
	changed, settled := false, false
	for k, st := range read {
		old, had := d.threads[k]
		if had && old == st {
			continue
		}
		changed = true
		if old.Settled != st.Settled {
			settled = true
			if st.Settled {
				d.log.Printf("T3 thread %s is settled, so its sessions are offline", k.thread)
			} else if had {
				d.log.Printf("T3 thread %s is no longer settled; its sessions are online again", k.thread)
			}
		}
		d.threads[k] = st
		delete(d.threadWarned, k)
	}
	for k, err := range failed {
		if !d.threadWarned[k] {
			d.threadWarned[k] = true
			d.log.Printf("can't read the state of T3 thread %s (%v); what was last read stands", k.thread, err)
		}
	}
	for k, st := range d.threads {
		if _, ok := routes[k]; !ok {
			delete(d.threads, k)
			delete(d.threadWarned, k)
			changed = changed || st.Settled
			settled = settled || st.Settled
		}
	}
	d.mu.Unlock()
	if settled {
		d.kickPresence()
	}
	if changed {
		d.kickPush()
	}
}

// threadStateLocked is what T3's database last said about the thread session id is in, and false
// if the session isn't in T3 or nothing has been read yet. Caller holds mu.
func (d *Daemon) threadStateLocked(id string) (t3db.State, bool) {
	r := d.routeOfLocked(id)
	if r.T3CLI == "" || r.T3Thread == "" {
		return t3db.State{}, false
	}
	st, ok := d.threads[threadKey{r.T3Home, r.T3Thread}]
	return st, ok
}

// settledLocked says session id is in a T3 thread that is settled, archived or deleted: it is
// offline, and takes no messages. Caller holds mu.
func (d *Daemon) settledLocked(id string) bool {
	st, ok := d.threadStateLocked(id)
	return ok && st.Settled
}

// offlineLocked says the session at dev/sess is offline because its T3 thread is settled: one here
// by T3's database, one on another device by that device's presence. Caller holds mu.
func (d *Daemon) offlineLocked(dev, sess string) bool {
	if sess == "" {
		return false
	}
	if dev == d.device {
		return d.settledLocked(sess)
	}
	p := d.peers[dev]
	return p != nil && !d.staleLocked(p) && slices.Contains(p.pres.Offline, sess)
}

// offlineIDsLocked lists the sessions here, running or stopped with their inbox kept, whose T3
// thread is settled, for presence. Caller holds mu.
func (d *Daemon) offlineIDsLocked() []string {
	var ids []string
	for _, id := range sortedKeys(d.sessions) {
		if d.settledLocked(id) {
			ids = append(ids, id)
		}
	}
	for _, id := range sortedKeys(d.st.T3Routes) {
		if d.sessions[id] == nil && d.receivesLocked(id) && d.settledLocked(id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// idleSinceLocked is when session id was last at work, as far as is known, and whether it is at
// work now. A session in T3 goes by T3's database: busy while a run is queued or running, else
// idle since its latest run. Any other session, or one in T3 whose thread hasn't been read, goes
// by the session: its start, its last request or tool call, or when it stopped. A message
// delivered as a turn counts too. Zero if nothing says. Caller holds mu.
func (d *Daemon) idleSinceLocked(id string) (since time.Time, busy bool) {
	since = d.pushedAt[id]
	if st, ok := d.threadStateLocked(id); ok {
		if st.Busy {
			return time.Time{}, true
		}
		return later(since, st.LastRun), false
	}
	if s := d.sessions[id]; s != nil {
		since = later(since, s.started, s.lastActive)
	}
	if at, ok := d.st.Stopped[id]; ok && d.sessions[id] == nil {
		since = later(since, at)
	}
	return since, false
}

// later is the latest of ts.
func later(ts ...time.Time) time.Time {
	var out time.Time
	for _, t := range ts {
		if t.After(out) {
			out = t
		}
	}
	return out
}

// choice is the page's wake setting for the session called name: its own, if it has one, else the
// device's.
func (p pageSettings) choice(name string) web.Wake {
	if o, ok := p.SessionWake[name]; ok && name != "" {
		return o
	}
	return p.Wake
}

// wakesLocked says whether a message may go into session id as a turn now. Not if its T3 thread is
// settled, nor if the page says never. Always: yes. Within N minutes (the default, and what an
// unset choice means): if the session is busy, which needs no waking, or was last at work no
// more than N minutes ago, 0 standing for defaultWakeMinutes. A session nothing is known of yet
// waits; the read of its thread lets the deliveries look again. Caller holds mu.
func (d *Daemon) wakesLocked(id string, wake pageSettings) bool {
	if d.settledLocked(id) {
		return false
	}
	w := wake.choice(d.sessionNameLocked(id))
	switch w.When {
	case web.WakeNever:
		return false
	case web.WakeAlways:
		return true
	}
	since, busy := d.idleSinceLocked(id)
	if busy {
		return true
	}
	limit := time.Duration(cmp.Or(w.Minutes, defaultWakeMinutes)) * time.Minute
	return !since.IsZero() && time.Since(since) <= limit
}
