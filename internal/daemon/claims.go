package daemon

import (
	"context"
	"errors"
	"fmt"
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
		if ok, _ := d.repo.IsAncestor(ctx, prev, sha); !ok {
			if back, _ := d.repo.IsAncestor(ctx, sha, prev); back {
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
	if rebuild {
		d.resetClaimsLocked()
	}
	now := time.Now()
	for _, c := range commits {
		seq := len(d.claimSeq)
		d.claimSeq[c.SHA] = seq
		var e bus.Event
		if bus.Decode(c.Message, &e) != nil || !e.Valid() {
			continue
		}
		switch e.Type {
		case bus.Claim:
			seen := now
			if restoring && e.At.Before(now) {
				seen = e.At
			}
			d.claims = append(d.claims, &claimRec{ev: e, seq: seq, seenAt: seen})
			d.noteLocked(e.At, e.Line())
		case bus.Release:
			// Only the holder's own device can release a claim.
			if rec := d.findClaimLocked(e.Claim); rec != nil && sameDevice(rec.ev.From, e.From) {
				rec.released = true
				d.noteLocked(e.At, e.Line())
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

// claimLiveLocked says whether a claim still holds. A claim ends when released, and lapses
// when its session is gone: missing from its device's presence, or that presence stale.
// Caller holds mu.
func (d *Daemon) claimLiveLocked(c *claimRec, now time.Time) bool {
	if c.released {
		return false
	}
	dev, sess := bus.SplitAddr(c.ev.From)
	if dev == d.device {
		return sess == proto.CLISession || d.sessions[sess] != nil
	}
	p := d.peers[dev]
	if p == nil {
		// No presence seen yet: give the device the same grace as a stale one.
		return now.Sub(c.seenAt) < d.opt.StaleAfter
	}
	if now.Sub(p.lastChange) > d.opt.StaleAfter {
		return false
	}
	if c.ev.At.After(p.pres.At) {
		return true // newer than the presence (both times from the same machine's clock)
	}
	for _, s := range p.pres.Sessions {
		if s.ID == sess {
			return true
		}
	}
	return false
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

// claim makes a claim for from, unless a live claim newer than the caller's seen token exists:
// then it returns those, unclaimed, so the agent decides with them in view.
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
	if err := d.syncClaims(ctx); err != nil {
		return fail("not claimed: can't reach the remote to check claims: " + err.Error())
	}
	for range casAttempts {
		d.mu.Lock()
		tip := d.claimsTip
		seq := -1
		if s, ok := d.claimSeq[req.Seen]; ok && req.Seen != "" {
			seq = s
		}
		unseen := d.openLocked(seq, from)
		all := d.openLocked(-1, "")
		d.mu.Unlock()
		if len(unseen) > 0 {
			return proto.Response{OK: true, Self: from, Unseen: claimViews(unseen), Claims: claimViews(all), Seen: tip,
				Note: "Nothing claimed: these claims are open and newer than your seen token."}
		}

		e := bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Claim, From: from, At: time.Now().UTC(),
			What: what, Paths: paths}
		sha, err := d.pushClaims(ctx, tip, e)
		if err == nil {
			d.mu.Lock()
			all := d.openLocked(-1, "")
			d.mu.Unlock()
			v := claimView(e)
			return proto.Response{OK: true, Self: from, Claimed: &v, Claims: claimViews(all), Seen: sha}
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
	return sha, d.advanceClaims(ctx, sha, false)
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
	ctx, cancel := context.WithTimeout(d.bgCtx, 2*time.Minute)
	defer cancel()
	for _, id := range held {
		if r := d.release(ctx, addr, id); r.Error != "" {
			d.log.Printf("releasing %s for stopped %s: %s", id, addr, r.Error)
		}
	}
}
