package daemon

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/a-Fig/t4/internal/brand"
	"github.com/a-Fig/t4/internal/bus"
	"github.com/a-Fig/t4/internal/names"
)

// Session names (#20). A session's name is two words (quiet-otter), unique in the repo and never
// reused. It is taken by compare-and-swap on one shared ref, refs/t4/names: each commit on it
// records one assignment, and a name is the session's only if the leased push of that commit
// lands. docs/VISION.md has the reasons.
//
// What a daemon holds, in state.json:
//   - Used: every name it has ever seen, from the registry's history or another device's
//     presence, with the session's address where known. It only grows, so a registry that is
//     overwritten, deleted or rewound frees no name.
//   - NameKeys: which name each of this device's session keys was given. The same key gets the same
//     name for good, across MCP server restarts, daemon restarts and resumes.
//   - NamesTip and NamesGood: the registry's tip as last examined, which is the lease for the next push, and
//     the last tip that was a commit and was read in full.

const (
	// nameTries is how many pushes in a row may be refused, because another device took the
	// registry's tip first, before the assigner backs off.
	nameTries      = 5
	maxNameBackoff = 5 * time.Minute
)

// nameKey is what a session's name follows: the client's own session ID when it gives one, so that
// a resume, or an MCP server restarting, is the same session; otherwise the ID t4 gave the session.
// The kinds are kept apart so that no ID of one can equal an ID of the other.
func nameKey(claude, id string) string {
	if claude != "" {
		return "claude:" + claude
	}
	return "session:" + id
}

// kickNames wakes the assigner.
func (d *Daemon) kickNames() {
	select {
	case d.nameKick <- struct{}{}:
	default:
	}
}

// noteNameLocked records that a name is in use, and by whom if that is known. It never takes one
// back, and an address already known is kept: the first claim a daemon saw stands. It reports
// whether anything changed. Caller holds mu.
func (d *Daemon) noteNameLocked(name, addr string) bool {
	cur, ok := d.st.Used[name]
	if ok && (cur != "" || addr == "") {
		return false
	}
	d.st.Used[name] = addr
	return true
}

// processNames reads the registry's tip into the set of used names. sha is the tip on the remote
// and already fetched, "" if the ref isn't there. Caller holds advMu.
//
// Anyone with push access can write any ref, so the tip may be junk, or a history that no longer
// holds what this daemon read before. Nothing read is ever forgotten; but the registry then no
// longer holds every name for daemons that join later, so the next commit this daemon writes there
// is a new root with a snapshot of every used name (see attemptName), and one is written without
// waiting for a session to need a name, if the registry is missing any.
func (d *Daemon) processNames(ctx context.Context, sha string) {
	d.mu.Lock()
	tip, good, checked := d.st.NamesTip, d.st.NamesGood, d.namesChecked
	d.mu.Unlock()
	if checked && tip == sha {
		return
	}
	fail := func(err error) {
		if ctx.Err() == nil {
			d.log.Printf("reading the names registry: %v", err)
		}
	}
	isCommit := false
	if sha != "" {
		ok, err := d.repo.IsCommit(ctx, sha)
		if err != nil {
			fail(err)
			return
		}
		isCommit = ok
	}
	var commits []bus.Commit
	disturbed := false
	if isCommit {
		from := ""
		if good != "" {
			// Descending from what was read, only what is new needs reading. Anything else is a
			// registry rewritten or rewound, read again from its start.
			desc, err := d.repo.IsAncestor(ctx, good, sha)
			if err != nil {
				fail(err)
				return
			}
			if desc {
				from = good
			} else {
				disturbed = true
			}
		}
		var err error
		if commits, err = d.repo.Log(ctx, from, sha); err != nil {
			fail(err)
			return
		}
	} else {
		// Junk, or the registry gone after having been read.
		disturbed = sha != "" || good != ""
	}
	read := map[string]string{}
	for _, c := range commits {
		var rec bus.NameRecord
		if bus.Decode(c.Message, &rec) != nil {
			continue
		}
		for name, addr := range rec.Claims() {
			if cur, ok := read[name]; !ok || cur == "" {
				read[name] = addr
			}
		}
	}

	d.mu.Lock()
	wasRepair := d.st.NamesRepair
	changed := d.st.NamesTip != sha
	for name, addr := range read {
		if d.noteNameLocked(name, addr) {
			changed = true
		}
	}
	d.st.NamesTip = sha
	if isCommit {
		d.st.NamesGood = sha
	}
	if disturbed {
		// Only a history read from its start shows whether the registry holds every name. A
		// read of what was added on top of a damaged one can't, so a repair still to be made
		// stays to be made.
		d.st.NamesRepair = false
		for name := range d.st.Used {
			if _, ok := read[name]; !ok {
				d.st.NamesRepair = true
				break
			}
		}
	}
	repair := d.st.NamesRepair
	changed = changed || repair != wasRepair
	d.namesChecked = true
	if changed {
		d.saveLocked()
	}
	d.mu.Unlock()
	if repair && !wasRepair {
		d.log.Printf("the names registry lost names; writing a snapshot of every name in use")
	}
	if repair {
		d.kickNames()
	}
}

// resolveNameLocked finds the session called name: first from presence, which follows a session
// whose ID changed, then from the registry, which also knows a session that has since stopped.
// Caller holds mu.
func (d *Daemon) resolveNameLocked(name string) (addr string, ok bool) {
	var dropped string
	for _, s := range d.sortedSessionsLocked() {
		if s.name != name {
			continue
		}
		if s.dropped.IsZero() {
			return bus.Addr(d.device, s.id), true
		}
		dropped = bus.Addr(d.device, s.id)
	}
	if dropped != "" {
		return dropped, true
	}
	for _, dev := range sortedKeys(d.peers) {
		for _, s := range d.peers[dev].pres.Sessions {
			if s.Name == name {
				return bus.Addr(dev, s.ID), true
			}
		}
	}
	addr = d.st.Used[name]
	if to := d.forwardLocked(addr); to != "" {
		addr = to // first given to a session that has since retired, and held now by another
	}
	return addr, addr != ""
}

// namesByAddrLocked inverts the registry: the name of each session whose address it knows.
// Caller holds mu.
func (d *Daemon) namesByAddrLocked() map[string]string {
	byAddr := map[string]string{}
	for name, addr := range d.st.Used {
		if addr != "" {
			byAddr[addr] = name
		}
	}
	return byAddr
}

// nameLoop gives names to the sessions that have none, and repairs the registry when it has lost
// names. A failure to reach the remote, or a registry too busy to take a push, leaves the
// sessions as they are, addressed as device/session, and is tried again after a delay that
// doubles up to maxNameBackoff.
func (d *Daemon) nameLoop(ctx context.Context) {
	retry := time.NewTimer(0) // after a restart, there may be a repair to finish
	defer retry.Stop()
	delay := d.opt.NameRetry
	failing := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.nameKick:
		case <-retry.C:
		}
		err := d.assignPending(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			if !failing {
				d.log.Printf("taking a session name failed, retrying: %v", err)
			}
			retry.Reset(delay)
			delay = min(delay*2, maxNameBackoff)
		case err == nil:
			if failing {
				d.log.Printf("session names working again")
			}
			delay = d.opt.NameRetry
		}
		failing = err != nil
	}
}

// wanted is a session that needs a name.
type wanted struct{ id, key string }

// nextName finds the next session without a name, and whether the registry needs repairing.
func (d *Daemon) nextName() (w *wanted, repair bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range d.sortedSessionsLocked() {
		if s.name == "" {
			return &wanted{id: s.id, key: s.key}, d.st.NamesRepair
		}
	}
	return nil, d.st.NamesRepair
}

// assignPending names every session that has no name and repairs the registry if it needs it. A
// push refused because another device moved the registry first is tried again with a fresh name,
// after reading what is new, up to nameTries times in a row.
func (d *Daemon) assignPending(ctx context.Context) error {
	refused := 0
	for ctx.Err() == nil {
		w, repair := d.nextName()
		if w == nil && !repair {
			return nil
		}
		err := d.attemptName(ctx, w)
		switch {
		case err == nil:
			refused = 0
		case errors.Is(err, bus.ErrRejected):
			if refused++; refused >= nameTries {
				return fmt.Errorf("the registry moved under %d pushes in a row", nameTries)
			}
			// Fetch, and merge what is new into the used names; the next try takes a fresh name.
			if err := d.pollOnce(ctx); err != nil {
				return err
			}
		default:
			return err
		}
	}
	return ctx.Err()
}

// pickName chooses a random name that is not known to be in use.
func (d *Daemon) pickName(used map[string]string) (string, error) {
	for range 1000 {
		name, err := names.Generate(d.nameSrc)
		if err != nil {
			return "", err
		}
		if _, taken := used[name]; !taken {
			return name, nil
		}
	}
	return "", errors.New("no unused name found")
}

// attemptName pushes one commit to the registry: the assignment of a fresh name to w (nil for a
// repair on its own), on the tip last read, leased on it. It lands only if no one else moved the
// registry since, which is what makes the name w's. When the tip isn't a commit, or the registry
// needs repairing, the commit is a new root holding a snapshot of every used name instead, leased
// on whatever the tip is. It returns bus.ErrRejected if the lease failed.
func (d *Daemon) attemptName(ctx context.Context, w *wanted) error {
	d.mu.Lock()
	tip, repair := d.st.NamesTip, d.st.NamesRepair
	used := maps.Clone(d.st.Used)
	d.mu.Unlock()

	rec := bus.NameRecord{V: bus.Version, At: time.Now().UTC()}
	if w != nil {
		name, err := d.pickName(used)
		if err != nil {
			return err
		}
		rec.Name, rec.Device, rec.Session = name, d.device, w.id
	}
	parent := tip
	if parent != "" {
		ok, err := d.repo.IsCommit(ctx, parent)
		if err != nil {
			return err
		}
		if !ok {
			parent = "" // junk is never something to build on
		}
	}
	if parent == "" || repair {
		parent, rec.Used = "", used
	}
	msg, err := bus.Encode(rec, rec.Subject())
	if err != nil {
		return err
	}
	sha, err := d.repo.NewCommit(ctx, parent, msg)
	if err != nil {
		return err
	}
	if err := d.repo.Push(ctx, sha, brand.NamesRef, bus.Lease, tip); err != nil {
		return err
	}

	if err := d.repo.UpdateLocal(ctx, brand.NamesRef, sha); err != nil {
		d.log.Printf("updating local %s: %v", brand.NamesRef, err)
	}
	d.advMu.Lock()
	defer d.advMu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.known[brand.NamesRef] = sha
	if d.st.NamesTip == tip {
		// Nothing newer was read meanwhile; this commit, on a tip that was read in full, is read.
		d.st.NamesTip, d.st.NamesGood, d.st.NamesRepair = sha, sha, false
	}
	if w != nil {
		addr := bus.Addr(d.device, w.id)
		d.noteNameLocked(rec.Name, addr)
		d.st.NameKeys[w.key] = rec.Name
		for _, s := range d.sessions {
			if s.key == w.key && s.name == "" {
				d.setNameLocked(s, rec.Name)
			}
		}
		d.noteLocked(rec.At, rec.Subject())
		d.log.Printf("session %s is named %s", addr, rec.Name)
	}
	d.saveLocked()
	if w != nil {
		d.kickPresence()
	}
	return nil
}
