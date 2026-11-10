package daemon

import (
	"context"
	"crypto/ecdh"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
)

// The team key (bus/team.go, #156). Every event and presence a device writes is encrypted for the
// current epoch's team key, whose public half anyone can read on the team ref, so a device can write
// before it holds any private half. A daemon that holds the team keys grants them, every one there
// has been, to each device it watches publish a sealed presence after the current epoch began and
// that hasn't been granted it: nobody runs anything. A new epoch is made by the first device to find
// the team ref empty, and by a rotation (`mur rotate`), wrapped for its writer alone; every other
// device gets it by a grant, which takes a presence pushed after the epoch, and so push access.

// teamEpoch is an epoch as this device has read it from the team ref, in the ref's order.
type teamEpoch struct {
	ID  string    `json:"id"`
	Pub string    `json:"pub"`
	By  string    `json:"by"`
	At  time.Time `json:"at"`
	// Collaborators is what the epoch recorded of the repo's collaborators, when this device could
	// open it.
	Collaborators []string `json:"collaborators,omitempty"`
}

// errNoTeamKey says a record can't be encrypted yet: no epoch has been read or made. The first sync
// makes one when the team ref is empty, so this lasts seconds.
var errNoTeamKey = errors.New("the team's encryption key isn't set up yet; Murmur makes it at the first sync")

// errStalled stops reading a log at a record encrypted for a team key this device is waiting for.
var errStalled = errors.New("waiting for the team key")

// teamWaitRepush is how often a device that lacks the current team key pushes its presence, instead
// of every Heartbeat: a grant waits for a presence pushed after the epoch, seen to arrive.
const teamWaitRepush = 10 * time.Second

// teamKeysPath is where this device keeps the team private keys it has been granted, by TeamID. Like
// keys, never committed and never on the bus; it only grows, so a rewritten team ref takes nothing
// this device could read away from it.
func teamKeysPath(dir string) string { return filepath.Join(dir, "team-keys") }

func loadTeamKeys(dir string) (bus.Keyring, error) {
	keys := bus.Keyring{}
	b, err := os.ReadFile(teamKeysPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return keys, nil
	}
	if err != nil {
		return nil, err
	}
	var raw map[string]string
	if err := json.Unmarshal(b, &raw); err != nil {
		return keys, nil // unreadable: granted again, by a teammate, like a device that is new
	}
	for id, h := range raw {
		b, err := hex.DecodeString(h)
		if err != nil {
			continue
		}
		if k, err := ecdh.X25519().NewPrivateKey(b); err == nil && bus.TeamID(bus.BoxPublic(k)) == id {
			keys[id] = k
		}
	}
	return keys, nil
}

// addTeamKeysLocked keeps keys this device was granted, saving them, and reports whether any was
// new. Caller holds mu.
func (d *Daemon) addTeamKeysLocked(keys bus.Keyring) bool {
	added := false
	for id, k := range keys {
		if _, ok := d.teamKeys[id]; !ok && bus.IsTeamID(id) {
			d.teamKeys[id] = k
			added = true
		}
	}
	if !added {
		return false
	}
	raw := map[string]string{}
	for id, k := range d.teamKeys {
		raw[id] = hex.EncodeToString(k.Bytes())
	}
	b, err := json.Marshal(raw)
	if err == nil {
		err = writeFileAtomic(teamKeysPath(d.dir), append(b, '\n'))
	}
	if err != nil {
		d.log.Printf("saving the team keys: %v", err)
	}
	// What this device couldn't open before may open now: every presence is read again, and the
	// logs, which stopped at the first record for a key this device lacked, go on (readEventsLocked).
	for _, p := range d.peers {
		if p.locked {
			p.sha = ""
		}
	}
	d.kickPoll()
	d.kickPresence() // its own presence can now be read back, and the faster re-push stops
	return true
}

// keyringLocked is every key this device opens boxes with: the team keys, and its own box key for
// what was encrypted for it alone. Caller holds mu.
func (d *Daemon) keyringLocked() bus.Keyring {
	ring := bus.Keyring{d.device: d.boxKey}
	for id, k := range d.teamKeys {
		ring[id] = k
	}
	return ring
}

// epochLocked is the current epoch, the last on the team ref, or nil before there is one. Caller
// holds mu.
func (d *Daemon) epochLocked() *teamEpoch {
	if n := len(d.st.Epochs); n > 0 {
		return &d.st.Epochs[n-1]
	}
	return nil
}

// knownEpochLocked says whether id is an epoch on the team ref. Caller holds mu.
func (d *Daemon) knownEpochLocked(id string) bool {
	return slices.ContainsFunc(d.st.Epochs, func(e teamEpoch) bool { return e.ID == id })
}

// lacksTeamKeyLocked says whether this device lacks the current epoch's key: it can write, and is
// waiting for a grant to read. Caller holds mu.
func (d *Daemon) lacksTeamKeyLocked() bool {
	ep := d.epochLocked()
	return ep != nil && d.teamKeys[ep.ID] == nil
}

// teamRecipientsLocked is what a record is encrypted for: the current team key, or, before there is
// one, this device alone, so that nothing it writes goes out in the clear. Caller holds mu.
func (d *Daemon) teamRecipientsLocked() map[string]string {
	if ep := d.epochLocked(); ep != nil {
		return map[string]string{ep.ID: ep.Pub}
	}
	return map[string]string{d.device: bus.BoxPublic(d.boxKey)}
}

// signalTeamLocked wakes what waits for the team ref to change (awaitEpoch). Caller holds mu.
func (d *Daemon) signalTeamLocked() {
	close(d.teamChanged)
	d.teamChanged = make(chan struct{})
}

// awaitEpoch waits until there is an epoch to encrypt for, or ctx ends.
func (d *Daemon) awaitEpoch(ctx context.Context) error {
	for {
		d.mu.Lock()
		ep, ch := d.epochLocked(), d.teamChanged
		d.mu.Unlock()
		if ep != nil {
			return nil
		}
		d.kickTeam()
		select {
		case <-ch:
		case <-ctx.Done():
			return errNoTeamKey
		}
	}
}

func (d *Daemon) kickTeam() {
	select {
	case d.teamKick <- struct{}{}:
	default:
	}
}

// processTeam reads the team ref up to sha: from where it last read, or from its root if the ref was
// rewritten. Caller holds advMu.
func (d *Daemon) processTeam(ctx context.Context, sha string) {
	d.mu.Lock()
	tip := d.st.TeamTip
	d.mu.Unlock()
	if sha == tip {
		return
	}
	from := tip
	if tip != "" {
		ok, err := d.repo.IsAncestor(ctx, tip, sha)
		if err != nil {
			d.log.Printf("reading %s: %v", brand.TeamRef, err)
			return
		}
		if !ok {
			// Rewritten: read it whole again. The keys this device holds stay (teamKeysPath).
			d.log.Printf("%s was rewritten; reading it from its start", brand.TeamRef)
			from = ""
		}
	}
	err := d.repo.Log(ctx, from, sha, d.maxRecord, func(commits []bus.Commit) error {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.applyPageLocked(func() {
			if from == "" && d.st.TeamTip == tip {
				d.st.Epochs, d.st.TeamGranted = nil, map[string][]string{}
			}
			for _, c := range commits {
				d.applyTeamLocked(c)
			}
			d.st.TeamTip = commits[len(commits)-1].SHA
		})
		return nil
	})
	if err != nil {
		d.log.Printf("reading %s: %v", brand.TeamRef, err)
		return
	}
	d.mu.Lock()
	if d.st.TeamTip != sha {
		d.st.TeamTip = sha // a tip that isn't a commit, or holds none Murmur reads
		d.saveLocked()
	}
	d.mu.Unlock()
	d.kickTeam()
}

// applyTeamLocked takes one record of the team ref. Only a record sealed for the team ref, by a
// device whose pinned key it was sealed with if that device is pinned, is taken; anything else is
// junk and passed over. Caller holds mu.
func (d *Daemon) applyTeamLocked(c bus.Commit) {
	if c.Oversize {
		return
	}
	state, seal := bus.OpenSeal(c.Message, brand.TeamRef)
	if state != bus.Sealed {
		d.log.Printf("%s: passing over %s, which isn't sealed", brand.TeamRef, short(c.SHA))
		return
	}
	if pin := d.st.Pins[seal.By]; pin != nil && pin.Sign != seal.Key {
		d.log.Printf("%s: passing over %s, sealed with a key that isn't the one pinned for %s", brand.TeamRef, short(c.SHA), seal.By)
		return
	}
	var r bus.TeamRecord
	if bus.Decode(c.Message, &r) != nil || !r.Valid() {
		return
	}
	grant := func(dev string, ids []string) {
		have := d.st.TeamGranted[dev]
		for _, id := range ids {
			if !slices.Contains(have, id) {
				have = append(have, id)
			}
		}
		d.st.TeamGranted[dev] = have
	}
	switch r.Type {
	case bus.TeamEpoch:
		if d.knownEpochLocked(r.ID) {
			return
		}
		ep := teamEpoch{ID: r.ID, Pub: r.Pub, By: seal.By, At: r.At}
		for _, dev := range r.Box.Recipients() {
			grant(dev, []string{r.ID})
		}
		if keys, collab, err := r.Open(d.device, d.boxKey); err == nil {
			d.addTeamKeysLocked(keys)
			ep.Collaborators = collab
		} else if !errors.Is(err, bus.ErrNotForUs) {
			d.log.Printf("%s: epoch %s doesn't open: %v", brand.TeamRef, r.ID, err)
		}
		d.st.Epochs = append(d.st.Epochs, ep)
		d.epochSeen[r.ID] = time.Now()
		d.signalTeamLocked()
		// This device's presence goes out again for the new key: before the first epoch it was
		// encrypted for this device alone, and after a rotation the old key may be one a removed
		// collaborator holds.
		d.kickPresence()
	case bus.TeamGrant:
		grant(r.To, r.IDs)
		if r.To != d.device {
			return
		}
		keys, _, err := r.Open(d.device, d.boxKey)
		if err != nil {
			d.log.Printf("%s: a grant to this device from %s doesn't open: %v", brand.TeamRef, seal.By, err)
			return
		}
		if d.addTeamKeysLocked(keys) {
			d.log.Printf("granted the team keys by %s", seal.By)
		}
		// An epoch this device holds the key of, without having opened its own record, has its
		// collaborators read from that record the next time it is read; they matter only to rotate.
	}
}

// teamLoop makes the team ref's records: the first epoch, when there is none, and grants. It runs
// when kicked (after each sync, and when the team ref was read), and retries a push that fails.
func (d *Daemon) teamLoop(ctx context.Context) {
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	defer retry.Stop()
	delay := d.opt.NameRetry
	failing := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.teamKick:
		case <-retry.C:
		}
		err := d.teamWork(ctx, nil)
		switch {
		case err != nil && ctx.Err() == nil:
			if !failing {
				d.log.Printf("writing %s failed, retrying: %v", brand.TeamRef, err)
			}
			retry.Reset(delay)
			delay = min(delay*2, maxNameBackoff)
		case err == nil:
			delay = d.opt.NameRetry
		}
		failing = err != nil
	}
}

// grantsDueLocked is the devices this one grants the team keys to now: each with a pinned box key
// that hasn't been given the current epoch, and that this daemon watched publish a sealed presence
// after it knew of the epoch, within StaleAfter. That is a device that can push now. A presence put
// back on its ref, or one from before the epoch, isn't one. Only a device that holds the current key
// grants. Caller holds mu.
func (d *Daemon) grantsDueLocked() []string {
	ep := d.epochLocked()
	if ep == nil || d.teamKeys[ep.ID] == nil {
		return nil
	}
	seen := d.epochSeen[ep.ID]
	var due []string
	for _, dev := range sortedKeys(d.peers) {
		p, pin := d.peers[dev], d.st.Pins[dev]
		if pin == nil || pin.Box == "" || slices.Contains(d.st.TeamGranted[dev], ep.ID) {
			continue
		}
		if p.watched.IsZero() || !p.watched.After(seen) || time.Since(p.watched) > d.opt.StaleAfter {
			continue
		}
		due = append(due, dev)
	}
	return due
}

// rotation is a request for a new epoch, with the collaborators it is to record.
type rotation struct{ collaborators []string }

// teamWork pushes what the team ref needs from this device, all in one leased push: a new epoch, if
// there is none or rot asks for one, and a grant for each device that needs one. A push refused
// because the ref moved is left to the next sync, which reads what moved it, and decides again.
func (d *Daemon) teamWork(ctx context.Context, rot *rotation) error {
	d.teamMu.Lock()
	defer d.teamMu.Unlock()
	d.mu.Lock()
	tip := d.st.TeamTip
	_, listed := d.known[brand.TeamRef]
	synced := !d.lastPollOK.IsZero()
	ep := d.epochLocked()
	var recs []bus.TeamRecord
	var newKey *ecdh.PrivateKey
	now := time.Now()
	if rot != nil || ep == nil && synced && !listed && tip == "" {
		// The epoch is wrapped for this device alone. Every other device gets it by a grant, once it
		// has pushed a presence after this: what a collaborator who has lost push access can't do.
		newKey = bus.NewBoxKey()
		var collab []string
		if rot != nil {
			collab = rot.collaborators
		}
		r, err := bus.NewEpoch(newKey, map[string]string{d.device: bus.BoxPublic(d.boxKey)}, collab, now)
		if err != nil {
			d.mu.Unlock()
			return err
		}
		recs = append(recs, r)
	}
	for _, dev := range d.grantsDueLocked() {
		r, err := bus.NewGrant(dev, d.st.Pins[dev].Box, d.teamKeys, now)
		if err != nil {
			d.mu.Unlock()
			return err
		}
		recs = append(recs, r)
	}
	d.mu.Unlock()
	if len(recs) == 0 {
		return nil
	}
	parent := tip
	if parent != "" {
		if ok, err := d.repo.IsCommit(ctx, parent); err != nil {
			return err
		} else if !ok {
			parent = ""
		}
	}
	sha := parent
	var shas, msgs []string
	for _, r := range recs {
		msg, err := bus.EncodeSealed(r, r.Subject(), d.signKey, d.device, brand.TeamRef)
		if err != nil {
			return err
		}
		if sha, err = d.repo.NewCommit(ctx, sha, msg); err != nil {
			return err
		}
		shas, msgs = append(shas, sha), append(msgs, msg)
	}
	if err := d.push(ctx, sha, brand.TeamRef, bus.Lease, tip); err != nil {
		if errors.Is(err, bus.ErrRejected) {
			d.kickPoll() // someone else wrote first: read it, then decide again
			return nil
		}
		return err
	}
	if err := d.repo.UpdateLocal(ctx, brand.TeamRef, sha); err != nil {
		d.log.Printf("updating local %s: %v", brand.TeamRef, err)
	}
	d.advMu.Lock()
	defer d.advMu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.known[brand.TeamRef] = sha
	if d.st.TeamTip != tip {
		return nil // read past meanwhile: the sync that did has what was pushed, or will
	}
	if newKey != nil {
		d.addTeamKeysLocked(bus.Keyring{recs[0].ID: newKey})
	}
	for i, r := range recs {
		d.applyTeamLocked(bus.Commit{SHA: shas[i], Message: msgs[i]})
		if r.Type == bus.TeamEpoch {
			d.log.Printf("made team key %s", r.ID)
			if rot != nil {
				d.st.Epochs[len(d.st.Epochs)-1].Collaborators = rot.collaborators
			}
		} else {
			d.log.Printf("granted the team keys to %s", r.To)
		}
	}
	d.st.TeamTip = sha
	d.saveLocked()
	return nil
}

// rotate makes a new epoch now, recording collaborators, and waits for it to land. Only a device that
// holds every team key may: the devices it grants to get the keys it holds, and history must stay
// readable to them.
func (d *Daemon) rotate(ctx context.Context, collaborators []string) (string, error) {
	d.mu.Lock()
	before := d.epochLocked()
	for _, ep := range d.st.Epochs {
		if d.teamKeys[ep.ID] == nil {
			d.mu.Unlock()
			return "", errors.New("this device hasn't been granted every team key yet, so it can't rotate; run it on a teammate's device, or wait for the grant")
		}
	}
	d.mu.Unlock()
	for attempt := 0; attempt < 3; attempt++ {
		if err := d.teamWork(ctx, &rotation{collaborators: collaborators}); err != nil {
			return "", err
		}
		d.mu.Lock()
		ep := d.epochLocked()
		d.mu.Unlock()
		if ep != nil && (before == nil || ep.ID != before.ID) {
			return ep.ID, nil
		}
		// Refused: another device wrote first. Read what it wrote, and try again on it.
		if err := d.pollOnce(ctx); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("%s kept moving; try again", brand.TeamRef)
}

// rotateRequest is OpRotate: a new team key if anyone the current epoch recorded as a collaborator
// is no longer in collab, or if force. With no epoch, or one that recorded no collaborators, there is
// nothing to compare against, so it rotates, and the new epoch records collab for next time. It
// returns what it did, for the person who asked.
func (d *Daemon) rotateRequest(ctx context.Context, collab []string, force bool) (string, error) {
	d.mu.Lock()
	var prev []string
	if ep := d.epochLocked(); ep != nil {
		prev = ep.Collaborators
	}
	d.mu.Unlock()
	var gone []string
	for _, login := range prev {
		if !slices.Contains(collab, login) {
			gone = append(gone, login)
		}
	}
	switch {
	case force:
	case collab == nil:
		return "", errors.New("couldn't list the repo's collaborators, so there is nothing to compare; --force makes a new team key anyway")
	case prev != nil && len(gone) == 0:
		return "Nobody has left since the team key was made, so it stays as it is.", nil
	}
	id, err := d.rotate(ctx, collab)
	if err != nil {
		return "", err
	}
	note := "Made a new team key, " + id + "."
	if len(gone) > 0 {
		note += " No longer collaborators: " + strings.Join(gone, ", ") + "."
	}
	return note + " Teammates' devices that can still push get it within a couple of minutes, and keep every key " +
		"before it; a device that can't push never gets it, and reads nothing written from now on.", nil
}
