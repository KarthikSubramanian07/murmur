package daemon

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/a-Fig/t4/internal/brand"
	"github.com/a-Fig/t4/internal/bus"
	"github.com/a-Fig/t4/internal/proto"
	"github.com/a-Fig/t4/internal/scrub"
)

// Claims live on one shared ref used as a compare-and-swap (docs/VISION.md, "Claims are a
// compare-and-swap"): every claim is pushed with a lease on the tip it was built on, so the
// remote accepts it only if nobody claimed in between.

// casAttempts bounds retries when other devices keep moving the claims ref.
const casAttempts = 5

// advanceClaims reads the claims ref up to sha. restoring marks a rebuild from local refs at
// startup, when a claim's own timestamp is the best guess at when it was first seen. Caller
// holds advMu.
func (d *Daemon) advanceClaims(ctx context.Context, sha string, restoring bool) error {
	d.mu.Lock()
	prev := d.claimsTip
	d.mu.Unlock()
	if prev == sha {
		return nil
	}
	rebuild := prev == ""
	if prev != "" {
		ok, err := d.repo.IsAncestor(ctx, prev, sha)
		if err != nil {
			return err
		}
		if !ok {
			back, err := d.repo.IsAncestor(ctx, sha, prev)
			if err != nil {
				return err
			}
			if back {
				return nil // an older copy of history already read
			}
			rebuild = true // the ref was rewritten; start over from what it says now
		}
	}
	from := prev
	if rebuild {
		from = ""
	}
	commits, err := d.repo.Log(ctx, from, sha)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	// A rebuild keeps what this daemon already knew about each claim: when it first saw it, what
	// tokens it was shown under, and which ends it already reported.
	var before map[string]*claimRec
	if rebuild {
		before = map[string]*claimRec{}
		for _, c := range d.claims {
			before[c.ev.ID] = c
		}
		d.resetClaimsLocked()
	}
	now := time.Now()
	for _, c := range commits {
		seq := len(d.claimSeq)
		d.claimSeq[c.SHA] = seq
		var e bus.Event
		if bus.Decode(c.Message, &e) != nil {
			continue
		}
		if e.Clean(); !e.Valid() {
			continue
		}
		switch e.Type {
		case bus.Claim:
			seen := now
			if restoring && e.At.Before(now) {
				seen = now.Add(-now.Sub(e.At)) // from now, to keep a monotonic reading
			}
			rec := &claimRec{ev: e, seq: seq, seenAt: seen}
			old := before[e.ID]
			if old != nil {
				rec.seenAt, rec.checked, rec.live, rec.liveSince = old.seenAt, old.checked, old.live, old.liveSince
			} else {
				d.noteLocked(e.At, e.Line())
			}
			d.claims = append(d.claims, rec)
			for _, id := range e.Ends {
				if ended := d.findClaimLocked(id); ended != nil && !ended.released {
					ended.released = true
					if was := before[id]; !restoring && (was == nil || !was.released) {
						d.tellEndedLocked(ended.ev, e)
					}
				}
			}
		case bus.Release:
			// Only the holder's own device can release a claim.
			if rec := d.findClaimLocked(e.Claim); rec != nil && sameDevice(rec.ev.From, e.From) {
				rec.released = true
				if was := before[e.Claim]; was == nil || !was.released {
					d.noteLocked(e.At, e.Line())
				}
			}
		}
	}
	d.claimsTip = sha
	return nil
}

func (d *Daemon) resetClaimsLocked() {
	d.claims = nil
	d.claimSeq = map[string]int{}
	d.claimsTip = ""
}

// syncClaims brings the claims ref up to date with the remote, outside the poll cycle, so that
// `claims` and `claim` work from the freshest view.
func (d *Daemon) syncClaims(ctx context.Context) error {
	sha, err := d.repo.RemoteRef(ctx, brand.ClaimsRef)
	if err != nil {
		return err
	}
	d.advMu.Lock()
	defer d.advMu.Unlock()
	if sha == "" {
		d.mu.Lock()
		d.resetClaimsLocked()
		d.mu.Unlock()
		return nil
	}
	d.mu.Lock()
	have := d.known[brand.ClaimsRef]
	d.mu.Unlock()
	if have != sha {
		if err := d.repo.Fetch(ctx, []string{brand.ClaimsRef}); err != nil {
			return err
		}
		d.mu.Lock()
		d.known[brand.ClaimsRef] = sha
		d.mu.Unlock()
	}
	return d.advanceClaims(ctx, sha, false)
}

// claimLiveLocked says whether a claim still holds, as far as this daemon can tell. A claim ends
// when it is released or a later claim ends it, and lapses when its session is gone: missing
// from its device's presence, or that presence stale or deleted. A claim made from the CLI ends
// with the daemon it was made under. Caller holds mu.
//
// A lapse is one daemon's judgement, and it can be undone: a laptop that wakes brings its claims
// back. That harms nobody unless someone claimed in the meantime without being shown them. So a
// claim lists the claims its daemon judged lapsed (Event.Ends), and every daemon treats those as
// ended for good: a lapse becomes final, and shared, the moment anyone claims past it.
func (d *Daemon) claimLiveLocked(c *claimRec, now time.Time) bool {
	if c.released {
		return false
	}
	dev, sess := bus.SplitAddr(c.ev.From)
	if dev == d.device {
		if sess == proto.CLISession {
			return !c.ev.At.Before(d.started)
		}
		if d.sessions[sess] != nil {
			return true
		}
		// Just after a restart, a session that held the claim under the last daemon may still
		// reconnect; until then, a sibling's claim mustn't end it.
		return c.ev.At.Before(d.started) && now.Sub(d.started) < catchUpWindow
	}
	p := d.peers[dev]
	if p == nil {
		if goneAt, ok := d.gone[dev]; ok && !c.seenAt.After(goneAt) {
			return false // made before its device said goodbye
		}
		// No presence seen yet, or a claim made since the goodbye (a restart): give the device
		// the same grace as a stale one.
		return now.Sub(c.seenAt) < d.opt.StaleAfter
	}
	if d.staleLocked(p) {
		return false
	}
	if c.ev.At.After(p.pres.At) {
		return true // newer than the presence (both times from the same machine's clock)
	}
	for _, s := range p.pres.Sessions {
		if s.ID == sess {
			// The CLI is always present; its claims are those made since its daemon started.
			return sess != proto.CLISession || !c.ev.At.Before(s.Started)
		}
	}
	return false
}

// lapsedLocked lists the claims this daemon judges lapsed: not released, but not live either.
// A claim made now ends them for everyone. Caller holds mu.
func (d *Daemon) lapsedLocked(now time.Time) []string {
	var ids []string
	for _, c := range d.claims {
		if !c.released && !d.claimLiveLocked(c, now) {
			ids = append(ids, c.ev.ID)
		}
	}
	return ids
}

// claimsHeldLocked counts the live claims held by addr. Caller holds mu.
func (d *Daemon) claimsHeldLocked(addr string) int {
	now, n := time.Now(), 0
	for _, c := range d.claims {
		if c.ev.From == addr && d.claimLiveLocked(c, now) {
			n++
		}
	}
	return n
}

// tellEndedLocked tells a session on this device that someone else's claim ended one of its
// own, which happens when this device looked gone to the claimer. Caller holds mu.
func (d *Daemon) tellEndedLocked(held, by bus.Event) {
	dev, sess := bus.SplitAddr(held.From)
	if dev != d.device {
		return
	}
	switch {
	case sess == proto.CLISession:
		if held.At.Before(d.started) {
			return // it ended with an earlier daemon anyway
		}
	case d.sessions[sess] == nil && time.Since(d.started) > 2*time.Minute:
		return // the session is gone; just after a restart it may still reconnect
	}
	when := "while this device looked gone to the team (asleep, offline or restarting)"
	if sameDevice(by.From, held.From) {
		when = "on this device, while your session wasn't connected to " + brand.Name
	}
	// A note speaks as t4, outside the envelope around other agents' words, so it carries none of
	// them: not a claim's description, nor even an address, which can spell words. IDs are hex.
	d.tellLocked(sess, fmt.Sprintf("Your claim %s was ended by claim %s, made %s. Check claims for who made it "+
		"and what it covers, and talk to them before carrying on.", held.ID, by.ID, when))
}

// endOldCLIClaims releases claims made from the CLI under an earlier daemon. Every daemon
// already treats them as ended; this records it, and tells the CLI's inbox.
func (d *Daemon) endOldCLIClaims() {
	if len(d.oldCLI) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(d.bgCtx, 2*time.Minute)
	defer cancel()
	for _, e := range d.oldCLI {
		// A failed release changes nothing for others, who end these claims by rule.
		if r := d.release(ctx, e.From, e.ID); r.Error != "" {
			d.log.Printf("releasing old CLI claim %s: %s", e.ID, r.Error)
		}
		d.mu.Lock()
		d.tellLocked(proto.CLISession, fmt.Sprintf("Your claim %s has ended: claims made from the CLI end "+
			"when its daemon stops. Claim it again if you still want it (%s log shows what it was).", e.ID, brand.Command))
		d.mu.Unlock()
	}
}

// tellLocked puts a message from t4 itself in a session's inbox. Caller holds mu.
func (d *Daemon) tellLocked(sess, subject string) {
	e := bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message, From: brand.Name,
		To: bus.Addr(d.device, sess), Subject: subject, At: time.Now().UTC()}
	d.st.Inbox[sess] = append(d.st.Inbox[sess], &inMsg{Event: e, Arrived: time.Now(), Note: true})
	d.saveLocked()
}

// A seen token names the claims tip an agent was shown and a generation, so that claim can tell
// what the agent wasn't shown: claims after that tip, and claims before it that were lapsed then
// and have come back since (a laptop that woke). Tokens are this daemon's own, never on the bus.
// The generation starts at the daemon's start time, so a token from an earlier daemon shows every
// open claim again, which costs the agent one more look.

// refreshLiveLocked records which claims are live now, and stamps each claim that came back from
// a lapse with a new generation. Caller holds mu.
func (d *Daemon) refreshLiveLocked(now time.Time) {
	for _, c := range d.claims {
		live := d.claimLiveLocked(c, now)
		switch {
		case live && !c.checked:
			c.liveSince = d.started.UnixNano()
		case live && !c.live:
			d.liveGen++
			c.liveSince = d.liveGen
		}
		c.checked, c.live = true, live
	}
}

// tokenLocked returns a seen token for the claims as they stand. Caller holds mu.
func (d *Daemon) tokenLocked(now time.Time) string {
	if d.claimsTip == "" {
		return "" // no claims yet; an empty token shows every claim made later
	}
	d.refreshLiveLocked(now)
	return d.claimsTip + "." + strconv.FormatInt(d.liveGen, 36)
}

// unseenLocked returns the live claims, other than exclude's, that whoever holds token wasn't
// shown. An empty or unknown token was shown nothing. Caller holds mu.
func (d *Daemon) unseenLocked(token, exclude string, now time.Time) []*claimRec {
	seq, gen := -1, int64(0)
	if sha, g, ok := strings.Cut(token, "."); ok {
		if s, known := d.claimSeq[sha]; known {
			seq = s
			gen, _ = strconv.ParseInt(g, 36, 64)
		}
	}
	d.refreshLiveLocked(now)
	var out []*claimRec
	for _, c := range d.claims {
		if c.ev.From != exclude && c.live && (c.seq > seq || c.liveSince > gen) {
			out = append(out, c)
		}
	}
	return out
}

// openLocked returns the live claims after position seq, leaving out those made by exclude.
func (d *Daemon) openLocked(seq int, exclude string) []*claimRec {
	now := time.Now()
	var open []*claimRec
	for _, c := range d.claims {
		if c.seq > seq && c.ev.From != exclude && d.claimLiveLocked(c, now) {
			open = append(open, c)
		}
	}
	return open
}

func (d *Daemon) findClaimLocked(id string) *claimRec {
	for _, c := range d.claims {
		if c.ev.ID == id {
			return c
		}
	}
	return nil
}

func sameDevice(a, b string) bool {
	da, _ := bus.SplitAddr(a)
	db, _ := bus.SplitAddr(b)
	return da == db
}

func claimViews(recs []*claimRec) []proto.ClaimView {
	views := make([]proto.ClaimView, 0, len(recs))
	for _, c := range recs {
		views = append(views, claimView(c.ev))
	}
	return views
}

func claimView(e bus.Event) proto.ClaimView {
	return proto.ClaimView{ID: e.ID, By: e.From, What: e.What, Paths: e.Paths, At: e.At}
}

// claim makes a claim for from, unless a live claim exists that the caller's seen token wasn't
// shown: then it returns those, unclaimed, so the agent decides with them in view.
func (d *Daemon) claim(ctx context.Context, from string, req proto.Request) proto.Response {
	what := bus.OneLine(req.What, 200)
	if what == "" {
		return fail("say what you are claiming")
	}
	var paths []string
	for _, p := range req.Paths {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	if err := scrub.Check(append([]string{what}, paths...)...); err != nil {
		return fail(err.Error())
	}
	d.claimMu.Lock()
	defer d.claimMu.Unlock()
	// Judge from fresh presence as well as fresh claims: a daemon just back from being offline
	// would otherwise take live devices for gone, and end their claims.
	if err := d.pollOnce(ctx); err != nil {
		return fail("not claimed: can't reach the remote to check claims: " + err.Error())
	}
	for range casAttempts {
		d.mu.Lock()
		now := time.Now()
		tip := d.claimsTip
		unseen := d.unseenLocked(req.Seen, from, now)
		all := d.openLocked(-1, "")
		ends := d.lapsedLocked(now)
		token := d.tokenLocked(now)
		d.mu.Unlock()
		if len(unseen) > 0 {
			return proto.Response{OK: true, Self: from, Unseen: claimViews(unseen), Claims: claimViews(all), Seen: token,
				Note: "Nothing claimed: these claims are open and your seen token didn't show them."}
		}

		e := bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Claim, From: from, At: time.Now().UTC(),
			What: what, Paths: paths, Ends: ends}
		_, err := d.pushClaims(ctx, tip, e)
		if err == nil {
			d.mu.Lock()
			all := d.openLocked(-1, "")
			token := d.tokenLocked(time.Now())
			d.mu.Unlock()
			v := claimView(e)
			return proto.Response{OK: true, Self: from, Claimed: &v, Claims: claimViews(all), Seen: token}
		}
		if !errors.Is(err, bus.ErrRejected) {
			return fail("not claimed: " + err.Error())
		}
		if err := d.syncClaims(ctx); err != nil {
			return fail("not claimed: can't reach the remote to check claims: " + err.Error())
		}
	}
	return fail("not claimed: the claims ref kept changing; try again")
}

// release ends a claim. Only the session that made it, or the CLI on its device, can.
func (d *Daemon) release(ctx context.Context, by, id string) proto.Response {
	d.claimMu.Lock()
	defer d.claimMu.Unlock()
	if err := d.syncClaims(ctx); err != nil {
		return fail("not released: can't reach the remote: " + err.Error())
	}
	for range casAttempts {
		d.mu.Lock()
		rec := d.findClaimLocked(id)
		tip := d.claimsTip
		d.mu.Unlock()
		if rec == nil {
			return fail("no claim " + id)
		}
		if rec.released {
			return proto.Response{OK: true, Self: by, Note: "claim " + id + " was already released"}
		}
		holder, _ := bus.SplitAddr(rec.ev.From)
		_, sess := bus.SplitAddr(by)
		if holder != d.device || rec.ev.From != by && sess != proto.CLISession {
			return fail(fmt.Sprintf("claim %s is held by %s; only that session, or the CLI on its device, can release it",
				id, rec.ev.From))
		}
		e := bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Release, From: by, At: time.Now().UTC(), Claim: id}
		_, err := d.pushClaims(ctx, tip, e)
		if err == nil {
			return proto.Response{OK: true, Self: by, Note: "released " + id}
		}
		if !errors.Is(err, bus.ErrRejected) {
			return fail("not released: " + err.Error())
		}
		if err := d.syncClaims(ctx); err != nil {
			return fail("not released: can't reach the remote: " + err.Error())
		}
	}
	return fail("not released: the claims ref kept changing; try again")
}

// pushClaims appends e to the claims ref on top of tip, as a compare-and-swap. Caller holds
// claimMu.
func (d *Daemon) pushClaims(ctx context.Context, tip string, e bus.Event) (string, error) {
	msg, err := bus.Encode(e, e.Line())
	if err != nil {
		return "", err
	}
	sha, err := d.repo.NewCommit(ctx, tip, msg)
	if err != nil {
		return "", err
	}
	if err := d.repo.Push(ctx, sha, brand.ClaimsRef, bus.Lease, tip); err != nil {
		return "", err
	}
	if err := d.repo.UpdateLocal(ctx, brand.ClaimsRef, sha); err != nil {
		d.log.Printf("updating local claims ref: %v", err)
	}
	d.advMu.Lock()
	defer d.advMu.Unlock()
	d.mu.Lock()
	d.known[brand.ClaimsRef] = sha
	d.mu.Unlock()
	// The push landed, so the claim or release holds whatever happens next; a failure to read
	// it back is caught up by the next sync.
	if err := d.advanceClaims(ctx, sha, false); err != nil {
		d.log.Printf("reading back claims after a push: %v", err)
	}
	return sha, nil
}

// releaseHeld releases every claim a stopped session held, so other devices see them end now
// rather than when presence catches up.
func (d *Daemon) releaseHeld(addr string) {
	d.mu.Lock()
	var held []string
	for _, c := range d.claims {
		if !c.released && c.ev.From == addr {
			held = append(held, c.ev.ID)
		}
	}
	d.mu.Unlock()
	if len(held) == 0 {
		return
	}
	// Not cancelled when the daemon starts stopping: it waits for these, and a claim left
	// unreleased would come back if a session with the same ID registered later.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(d.bgCtx), 20*time.Second)
	defer cancel()
	for _, id := range held {
		if r := d.release(ctx, addr, id); r.Error != "" {
			d.log.Printf("releasing %s for stopped %s: %s", id, addr, r.Error)
		}
	}
}
