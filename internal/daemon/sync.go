package daemon

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/a-Fig/t4/internal/brand"
	"github.com/a-Fig/t4/internal/bus"
	"github.com/a-Fig/t4/internal/gitx"
	"github.com/a-Fig/t4/internal/proto"
)

func (d *Daemon) pollLoop(ctx context.Context) {
	t := time.NewTicker(d.opt.Poll)
	defer t.Stop()
	failing := false
	for {
		err := d.pollOnce(ctx)
		switch {
		case err != nil && ctx.Err() == nil && !failing:
			d.log.Printf("sync failed, retrying every %s: %v", d.opt.Poll, err)
		case err == nil && failing:
			d.log.Printf("sync working again")
		}
		failing = err != nil
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// pollOnce lists the remote's bus refs and fetches and processes the ones that changed. The
// listing is one cheap round trip; objects move only when something changed.
func (d *Daemon) pollOnce(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	remote, err := d.repo.ListRemote(ctx)
	if err != nil {
		return err
	}
	d.advMu.Lock()
	defer d.advMu.Unlock()

	var changed []string
	d.mu.Lock()
	for ref, sha := range remote {
		if d.known[ref] != sha {
			changed = append(changed, ref)
		}
	}
	d.mu.Unlock()
	if err := d.repo.Fetch(ctx, changed); err != nil {
		return err
	}
	d.mu.Lock()
	for _, ref := range changed {
		d.known[ref] = remote[ref]
	}
	first := !d.st.Initialized
	d.mu.Unlock()

	for _, ref := range sortedKeys(remote) {
		sha := remote[ref]
		switch {
		case ref == brand.ClaimsRef:
			if err := d.advanceClaims(ctx, sha, false); err != nil {
				d.log.Printf("reading claims: %v", err)
			}
		case strings.HasPrefix(ref, brand.MeshPrefix):
			d.advanceMesh(ctx, ref, sha, first)
		case strings.HasPrefix(ref, brand.PresencePrefix):
			d.updatePeer(ctx, ref, sha)
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	for dev := range d.peers {
		if _, ok := remote[brand.PresencePrefix+dev]; !ok {
			delete(d.peers, dev) // its daemon said goodbye
		}
	}
	if _, ok := remote[brand.ClaimsRef]; !ok && d.claimsTip != "" {
		d.log.Printf("the claims ref is gone from the remote; forgetting all claims")
		d.resetClaimsLocked()
	}
	if first {
		d.st.Initialized = true
		d.saveLocked()
	}
	return nil
}

// advanceMesh processes a device's log up to sha and delivers its messages. On the first sync
// it only records the tip. Caller holds advMu.
func (d *Daemon) advanceMesh(ctx context.Context, ref, sha string, first bool) {
	d.mu.Lock()
	prev := d.st.Processed[ref]
	d.mu.Unlock()
	if prev == sha {
		return
	}
	skipTo := func() {
		d.mu.Lock()
		d.st.Processed[ref] = sha
		d.saveLocked()
		d.mu.Unlock()
	}
	if first {
		skipTo()
		return
	}
	if prev != "" {
		if ok, _ := d.repo.IsAncestor(ctx, prev, sha); !ok {
			if back, _ := d.repo.IsAncestor(ctx, sha, prev); back {
				return // an older copy of a log already read past
			}
			d.log.Printf("%s was rewritten; skipping its history rather than replaying it", ref)
			skipTo()
			return
		}
	}
	commits, err := d.repo.Log(ctx, prev, sha)
	if err != nil {
		d.log.Printf("reading %s: %v", ref, err)
		return
	}
	dev := strings.TrimPrefix(ref, brand.MeshPrefix)
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range commits {
		var e bus.Event
		if bus.Decode(c.Message, &e) != nil || !e.Valid() {
			continue
		}
		if from, _ := bus.SplitAddr(e.From); from != dev {
			continue // a device's log carries only its own sessions' events
		}
		d.noteLocked(e.At, e.Line())
		if e.Type == bus.Message {
			d.deliverLocked(e)
		}
	}
	d.st.Processed[ref] = sha
	d.saveLocked()
}

// deliverLocked puts a message in the inbox of each session here it is addressed to. A session
// gets only what arrives while it runs. Caller holds mu.
func (d *Daemon) deliverLocked(e bus.Event) {
	dev, sess := bus.SplitAddr(e.To)
	var to []string
	switch {
	case e.To == "*" || dev == d.device && sess == "":
		to = append(sortedKeys(d.sessions), proto.CLISession)
	case dev == d.device:
		if sess == proto.CLISession || d.sessions[sess] != nil {
			to = []string{sess}
		} else {
			d.log.Printf("message %s is for %s, which isn't running here; dropped", e.ID, e.To)
		}
	}
	for _, id := range to {
		if bus.Addr(d.device, id) == e.From {
			continue
		}
		box := append(d.st.Inbox[id], &inMsg{Event: e, Arrived: time.Now()})
		if len(box) > maxInbox {
			box = box[len(box)-maxInbox:]
		}
		d.st.Inbox[id] = box
	}
}

// updatePeer reads another device's presence. Caller holds advMu.
func (d *Daemon) updatePeer(ctx context.Context, ref, sha string) {
	dev := strings.TrimPrefix(ref, brand.PresencePrefix)
	if dev == d.device {
		return
	}
	d.mu.Lock()
	p := d.peers[dev]
	d.mu.Unlock()
	if p != nil && p.sha == sha {
		return
	}
	c, err := d.repo.ReadCommit(ctx, sha)
	if err != nil {
		d.log.Printf("reading %s: %v", ref, err)
		return
	}
	var pres bus.Presence
	if bus.Decode(c.Message, &pres) != nil || pres.V != bus.Version || pres.Device != dev {
		return
	}
	now := time.Now()
	changed := now
	if p == nil && pres.At.Before(now) {
		// First sight, so there's no change of ours to time it by: trust the writer's clock once.
		changed = pres.At
	}
	d.mu.Lock()
	d.peers[dev] = &peer{pres: pres, sha: sha, lastChange: changed}
	d.mu.Unlock()
}

// appendEvent adds an event to this device's log on the remote. It returns once the push has
// landed, so a sender knows whether its message went out.
func (d *Daemon) appendEvent(ctx context.Context, e bus.Event) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	ref := d.meshRef()
	msg, err := bus.Encode(e, e.Line())
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		sha, err := d.repo.NewCommit(ctx, d.ownTip, msg)
		if err != nil {
			return err
		}
		err = d.repo.Push(ctx, sha, ref, bus.FastForward, "")
		if err == nil {
			d.ownTip = sha
			if err := d.repo.UpdateLocal(ctx, ref, sha); err != nil {
				d.log.Printf("updating local %s: %v", ref, err)
			}
			d.advMu.Lock()
			d.mu.Lock()
			d.known[ref] = sha
			d.mu.Unlock()
			d.advanceMesh(ctx, ref, sha, false) // delivers to sessions on this device
			d.advMu.Unlock()
			return nil
		}
		if !errors.Is(err, bus.ErrRejected) || attempt > 0 {
			return err
		}
		// The remote's copy of this log is ahead of what this daemon knew: a restart before the
		// first sync, or a copied .git. Build on the remote's tip instead.
		tip, err := d.repo.RemoteRef(ctx, ref)
		if err != nil {
			return err
		}
		if err := d.repo.Fetch(ctx, []string{ref}); err != nil {
			return err
		}
		d.ownTip = tip
	}
}

// appendSoon appends an event in the background, for events nobody waits on.
func (d *Daemon) appendSoon(e bus.Event) {
	if d.bgCtx.Err() != nil {
		return
	}
	d.bg.Add(1)
	go func() {
		defer d.bg.Done()
		ctx, cancel := context.WithTimeout(d.bgCtx, time.Minute)
		defer cancel()
		if err := d.appendEvent(ctx, e); err != nil {
			d.log.Printf("writing %q: %v", e.Line(), err)
		}
	}()
}

func (d *Daemon) kickPresence() {
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

// presenceLoop pushes this device's presence when it changes, and on a heartbeat so others can
// tell it is still here.
func (d *Daemon) presenceLoop(ctx context.Context) {
	heartbeat := time.NewTicker(d.opt.Heartbeat)
	defer heartbeat.Stop()
	retry := time.NewTimer(0) // publish once at start
	defer retry.Stop()
	failing := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
		case <-retry.C:
		case <-d.kick:
			// Let a burst of changes (a session starting and setting its status) land as one push.
			select {
			case <-ctx.Done():
				return
			case <-time.After(300 * time.Millisecond):
			}
		}
		err := d.publishPresence(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			if !failing {
				d.log.Printf("pushing presence failed, retrying: %v", err)
			}
			retry.Reset(15 * time.Second)
		case err == nil && failing:
			d.log.Printf("presence pushed again")
		}
		failing = err != nil
		d.pruneInboxes()
	}
}

func (d *Daemon) publishPresence(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	d.mu.Lock()
	cwds := map[string]string{}
	for id, s := range d.sessions {
		cwds[id] = s.cwd
	}
	d.mu.Unlock()
	branches := map[string]string{}
	for id, cwd := range cwds {
		branches[id] = branchOf(ctx, cwd)
	}

	d.mu.Lock()
	for id, b := range branches {
		if s := d.sessions[id]; s != nil && b != "" {
			s.branch = b
		}
	}
	pres := bus.Presence{V: bus.Version, Device: d.device, Host: d.host, Owner: d.owner, At: time.Now().UTC()}
	for _, s := range d.sortedSessionsLocked() {
		pres.Sessions = append(pres.Sessions, bus.SessionInfo{
			ID: s.id, Agent: s.agent, Branch: s.branch, Doing: s.doing, Started: s.started.UTC()})
	}
	// The CLI is always present, so claims a human makes from it hold while the daemon runs.
	pres.Sessions = append(pres.Sessions, bus.SessionInfo{ID: proto.CLISession, Agent: "cli", Started: d.started.UTC()})
	d.mu.Unlock()

	msg, err := bus.Encode(pres, fmt.Sprintf("%s: %d sessions", d.device, len(pres.Sessions)-1))
	if err != nil {
		return err
	}
	sha, err := d.repo.NewCommit(ctx, "", msg)
	if err != nil {
		return err
	}
	return d.repo.Push(ctx, sha, d.presenceRef(), bus.Force, "")
}

// pruneInboxes drops inboxes left by sessions that didn't come back after a restart.
func (d *Daemon) pruneInboxes() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if time.Since(d.started) < 2*time.Minute {
		return
	}
	for id := range d.st.Inbox {
		if id != proto.CLISession && d.sessions[id] == nil {
			delete(d.st.Inbox, id)
			d.saveLocked()
		}
	}
}

func (d *Daemon) sortedSessionsLocked() []*session {
	list := make([]*session, 0, len(d.sessions))
	for _, s := range d.sessions {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].started.Before(list[j].started) })
	return list
}

// branchOf reads the branch checked out where a session works. It reads HEAD only; nothing
// touches that worktree's index.
func branchOf(ctx context.Context, cwd string) string {
	if cwd == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if out, err := gitx.RunIn(ctx, cwd, "symbolic-ref", "--short", "-q", "HEAD"); err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	if out, err := gitx.RunIn(ctx, cwd, "rev-parse", "--short", "HEAD"); err == nil {
		return "detached at " + strings.TrimSpace(out)
	}
	return ""
}
