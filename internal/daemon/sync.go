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
		if err != nil && ctx.Err() == nil {
			// A lock left by a killed git fails every fetch of its ref, and git doesn't always
			// say so in words ("reference already exists"); clearing stale ones is cheap.
			clearStaleLocks(d.opt.GitDir, d.log)
		}
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
	// A listing applied after a newer one would delete presence that has since appeared, and
	// mark its device gone.
	d.syncMu.Lock()
	defer d.syncMu.Unlock()
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
	if d.backlog == nil {
		// What the first listing shows is the backlog. A device that appears later is new, and
		// its log is read normally from the start.
		d.backlog = map[string]bool{}
		for ref := range remote {
			if d.mode != modeNormal && strings.HasPrefix(ref, brand.MeshPrefix) {
				d.backlog[ref] = true
			}
		}
	}
	for ref, sha := range remote {
		if d.known[ref] != sha {
			changed = append(changed, ref)
		}
	}
	d.mu.Unlock()
	if err := d.repo.Fetch(ctx, changed); err != nil {
		return err
	}
	if len(changed) > 0 {
		// A ref can move between the listing and the fetch. A presence ref is force-pushed, so
		// the commit listed may never have been fetched; going by it would leave that peer
		// unread and, soon, judged stale. Go by what the fetch brought.
		local, err := d.repo.LocalRefs(ctx)
		if err != nil {
			return err
		}
		for _, ref := range changed {
			if sha, ok := local[ref]; ok {
				remote[ref] = sha
			}
		}
	}
	// Monotonic, like every observation staleness is judged by, so a jump of the wall clock
	// makes nobody look gone; the wall clock only goes into state.json.
	polled := time.Now()
	wall := polled.Round(0)
	d.mu.Lock()
	for _, ref := range changed {
		d.known[ref] = remote[ref]
	}
	d.mu.Unlock()

	var unread []string
	for _, ref := range sortedKeys(remote) {
		sha := remote[ref]
		switch {
		case ref == brand.ClaimsRef:
			if err := d.advanceClaims(ctx, sha, false); err != nil {
				d.log.Printf("reading claims: %v", err)
			}
		case strings.HasPrefix(ref, brand.MeshPrefix):
			if strings.TrimPrefix(ref, brand.MeshPrefix) == brand.Name {
				continue // no device has that name; a log under it would only impersonate t4
			}
			d.advanceMesh(ctx, ref, sha)
		case strings.HasPrefix(ref, brand.PresencePrefix):
			if err := d.updatePeer(ctx, ref, sha); err != nil {
				d.log.Printf("%v", err)
				unread = append(unread, ref)
			}
		}
	}
	// A sync that didn't get to look at every device is no fresh look at any: it would make the
	// ones it missed look stale, and claim, which judges from it, would end their claims.
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(unread) > 0 {
		return fmt.Errorf("couldn't read %s", strings.Join(unread, ", "))
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	for dev := range d.peers {
		if _, ok := remote[brand.PresencePrefix+dev]; !ok {
			delete(d.peers, dev) // its daemon said goodbye, and its claims end now
			d.gone[dev] = time.Now()
		}
	}
	if _, ok := remote[brand.ClaimsRef]; !ok && d.claimsTip != "" {
		d.log.Printf("the claims ref is gone from the remote; forgetting all claims")
		d.resetClaimsLocked()
	}
	d.lastPollOK = polled
	switched := false
	if d.mode != modeNormal && len(d.backlog) == 0 {
		d.mode = modeNormal
		d.st.Initialized = true
		switched = true
	}
	if switched || d.mode == modeNormal && wall.Sub(d.st.LastSync) > time.Minute {
		d.st.LastSync = wall
		d.saveLocked()
	}
	return nil
}

// staleLocked says whether a peer's presence went unchanged past the stale limit, as of this
// daemon's last successful sync. A daemon that hasn't heard from the remote for a while (asleep,
// offline) has no grounds to call anyone else gone. Caller holds mu.
func (d *Daemon) staleLocked(p *peer) bool {
	return !d.lastPollOK.IsZero() && d.lastPollOK.Sub(p.lastChange) > d.opt.StaleAfter
}

// firstSight is when to treat a presence as last changed when this daemon has no observation
// of its own. The writer's clock is trusted once, but never so far back that the device would be
// judged stale before its next heartbeat could arrive: a peer whose clock runs slow would
// otherwise look gone, and a claim made then would end its claims for good. A device that
// really is gone takes 2 heartbeats longer to be called so by a reader that just started.
func (d *Daemon) firstSight(p bus.Presence) time.Time {
	now := time.Now()
	age := min(max(now.Sub(p.At), 0), max(d.opt.StaleAfter-2*d.opt.Heartbeat, 0))
	return now.Add(-age) // from now, to keep a monotonic reading
}

// advanceMesh processes a device's log up to sha and delivers its messages, as the sync mode
// says. Caller holds advMu.
func (d *Daemon) advanceMesh(ctx context.Context, ref, sha string) {
	d.mu.Lock()
	prev := d.st.Processed[ref]
	mode := modeNormal
	if d.backlog[ref] {
		mode = d.mode
	}
	d.mu.Unlock()
	settled := func() {
		d.mu.Lock()
		delete(d.backlog, ref)
		d.mu.Unlock()
	}
	if prev == sha {
		settled()
		return
	}
	skipTo := func() {
		d.mu.Lock()
		d.st.Processed[ref] = sha
		delete(d.backlog, ref)
		d.saveLocked()
		d.mu.Unlock()
	}
	if mode == modeSkip {
		// Nothing is delivered from the backlog, unless the first sync failed and sessions have
		// registered since: what was sent straight to them came after they started.
		d.mu.Lock()
		waiting := len(d.sessions) > 0
		d.mu.Unlock()
		if !waiting {
			skipTo()
			return
		}
	}
	if prev != "" {
		ok, err := d.repo.IsAncestor(ctx, prev, sha)
		if err != nil {
			d.log.Printf("reading %s: %v", ref, err)
			return
		}
		if !ok {
			back, err := d.repo.IsAncestor(ctx, sha, prev)
			if err != nil {
				d.log.Printf("reading %s: %v", ref, err)
				return
			}
			if back {
				settled() // an older copy of a log already read past
				return
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
		if bus.Decode(c.Message, &e) != nil {
			continue
		}
		if e.Clean(); !e.Valid() {
			continue
		}
		if from, _ := bus.SplitAddr(e.From); from != dev {
			continue // a device's log carries only its own sessions' events
		}
		if mode != modeSkip {
			d.noteLocked(e.At, e.Line())
		}
		if e.Type == bus.Message {
			d.deliverLocked(e, mode)
		}
	}
	d.st.Processed[ref] = sha
	delete(d.backlog, ref)
	d.saveLocked()
}

// deliverLocked puts a message in the inbox of each session here it is addressed to. A session
// gets only what arrives while it runs. What arrived while the daemon was down (modeCatchUp)
// goes only to sessions that were running before and reconnect, and in modeSkip to nobody; in
// both, what was sent after a session here started is for it. Everything
// is held for catchUpWindow after the daemon starts, for sessions still reconnecting (see
// catchUpLocked). Caller holds mu.
func (d *Daemon) deliverLocked(e bus.Event, mode syncMode) {
	held := mode == modeCatchUp || mode == modeNormal && time.Since(d.started) <= catchUpWindow
	if held {
		d.pending = append(d.pending, e)
	}
	ids := append(sortedKeys(d.sessions), proto.CLISession)
	for _, id := range ids {
		if !d.addressedLocked(e, id) {
			continue
		}
		if mode != modeNormal && !d.sentSinceLocked(e, id) &&
			(mode == modeSkip || id == proto.CLISession || !d.sessions[id].reconnect) {
			continue
		}
		d.addInboxLocked(id, e)
	}
	if dev, sess := bus.SplitAddr(e.To); mode == modeNormal && !held && dev == d.device && sess != "" &&
		sess != proto.CLISession && d.sessions[sess] == nil {
		d.log.Printf("message %s is for %s, which isn't running here; dropped", e.ID, e.To)
	}
}

// sentSinceLocked says whether e was sent after session id started, by the sender's clock, with
// a minute's allowance for it. Caller holds mu.
func (d *Daemon) sentSinceLocked(e bus.Event, id string) bool {
	s := d.sessions[id]
	return s != nil && !e.At.Before(s.started.Add(-time.Minute))
}

// addressedLocked says whether e is for session id on this device: sent to it, to this device,
// or to everyone, and not by it.
func (d *Daemon) addressedLocked(e bus.Event, id string) bool {
	addr := bus.Addr(d.device, id)
	return e.From != addr && (e.To == "*" || e.To == d.device || e.To == addr)
}

func (d *Daemon) addInboxLocked(id string, e bus.Event) {
	for _, m := range d.st.Inbox[id] {
		if m.Event.ID == e.ID {
			return
		}
	}
	box := append(d.st.Inbox[id], &inMsg{Event: e, Arrived: time.Now()})
	if len(box) > maxInbox {
		box = box[len(box)-maxInbox:]
	}
	d.st.Inbox[id] = box
}

// catchUpLocked gives a session that reconnects after a restart what arrived for it while the
// daemon was down. Caller holds mu.
func (d *Daemon) catchUpLocked(id string) {
	if time.Since(d.started) > catchUpWindow || d.caughtUp[id] {
		return
	}
	d.caughtUp[id] = true
	for _, e := range d.pending {
		if d.addressedLocked(e, id) {
			d.addInboxLocked(id, e)
		}
	}
	d.saveLocked()
}

// updatePeer reads another device's presence. One it can't read is an error. One it reads but
// can't make sense of (a newer version, say) still shows that the device is alive. Caller holds
// advMu.
func (d *Daemon) updatePeer(ctx context.Context, ref, sha string) error {
	dev := strings.TrimPrefix(ref, brand.PresencePrefix)
	if dev == d.device {
		return nil
	}
	d.mu.Lock()
	p := d.peers[dev]
	d.mu.Unlock()
	if p != nil && p.sha == sha {
		return nil
	}
	c, err := d.repo.ReadCommit(ctx, sha)
	if err != nil {
		return fmt.Errorf("reading %s: %w", ref, err)
	}
	var pres bus.Presence
	if bus.Decode(c.Message, &pres) != nil || pres.V != bus.Version || pres.Device != dev {
		if p != nil {
			d.mu.Lock()
			p.sha, p.lastChange = sha, time.Now()
			d.mu.Unlock()
		}
		return nil
	}
	pres.Clean()
	changed := time.Now()
	if p == nil {
		changed = d.firstSight(pres) // no change of ours to time it by
	}
	d.mu.Lock()
	d.peers[dev] = &peer{pres: pres, sha: sha, lastChange: changed}
	delete(d.gone, dev)
	d.mu.Unlock()
	return nil
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
			d.advanceMesh(ctx, ref, sha) // delivers to sessions on this device
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
	// Not cut short by shutdown: a push killed mid-flight can still land, after the goodbye.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
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
	if time.Since(d.started) > catchUpWindow {
		d.pending = nil
	}
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
