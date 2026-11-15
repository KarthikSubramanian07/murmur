package daemon

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/web"
)

// A device's colour (#573). The page fills every face on a device with that device's colour
// (#560). Its user can pick one on the page's Settings, from the palette's bus.Colors, as long as
// no other device holds it; presence carries the pick, inside the box, and every daemon settles
// who holds what by one rule (bus.DeviceColors), so every teammate's page shows the same colours.
// A device on a Murmur from before this sends no pick, and its colour is chosen for it.
// docs/design/presence.md ("Device colours") has the reasons.
//
// A pick that loses is given up, not kept: the device goes back to Auto, and its person is told
// who has the colour (st.ColorLostTo). It loses a race by the rule, and it loses on coming back
// to a colour picked while it was away: a daemon that stops for being idle takes its presence off
// the remote, so its colour looked free. Until its first full sync it leaves its pick out of its
// presence, and then gives it up if a peer has picked it meanwhile. Kept, a lost pick would come
// back each time the holder's daemon went idle, and two devices would swap colours day by day.
//
// A device that died without a goodbye leaves its presence, and its pick, on the remote (#581).
// Whether it is stale is each reader's own clock, so it doesn't end the pick; a pick made more
// than StaleAfter after that presence was written takes the colour (bus.DeviceColors), which every
// daemon settles alike from the two times in presence. A device that comes back from being quiet
// that long (asleep, or offline) settles its pick again as one that restarted does.

// colorsLocked is every device's colour, this one's included, by the devices in presence. A
// device whose presence is stale still holds its colour, until a pick made long enough after its
// presence takes it. Caller holds mu.
func (d *Daemon) colorsLocked() map[string]bus.DeviceColor {
	color, at := d.shownColorLocked() // as the peers see it
	return bus.DeviceColors(d.colorPicksLocked(color, at), d.opt.StaleAfter)
}

// colorPicksLocked is every device's pick, this one's as color and at, each with when its
// presence on the remote was written. Caller holds mu.
func (d *Daemon) colorPicksLocked(color int, at time.Time) []bus.ColorPick {
	picks := []bus.ColorPick{{Device: d.device, Color: color, At: at, Seen: d.presenceOut}}
	for id, p := range d.peers {
		picks = append(picks, peerPick(id, p))
	}
	return picks
}

func peerPick(id string, p *peer) bus.ColorPick {
	return bus.ColorPick{Device: id, Color: p.pres.Color, At: p.pres.ColorAt, Seen: p.pres.At}
}

// shownColorLocked is the pick this device's presence carries: none until its first full sync
// has settled it. Caller holds mu.
func (d *Daemon) shownColorLocked() (int, time.Time) {
	if !d.colorSettled {
		return 0, time.Time{}
	}
	return d.st.Color, d.st.ColorAt
}

// settleColorLocked gives up this device's pick if a peer holds that colour: one bus.DeviceColors
// gives it to, or, at the first full sync, one that picked it while this device was away, not one
// this device took it from for being quiet. Called after each full sync. The pick stays out of
// presence while any peer's is locked (no team key for it yet), since it could hide the holder; a
// lost pick is still given up meanwhile. Caller holds mu.
func (d *Daemon) settleColorLocked() {
	if d.st.Color != 0 {
		if id := d.colorHolderLocked(); id != "" {
			d.st.Color, d.st.ColorAt, d.st.ColorLostTo = 0, time.Time{}, d.deviceCalledLocked(id)
			d.saveLocked()
			d.kickPresence()
		}
	}
	for _, p := range d.peers {
		// A live holder could be hiding in it: settled once every live presence can be read. A
		// stale one may never open (written before its device read a team key), and whether to
		// hold this device's own pick back is this device's call, so its clock may judge.
		if p.locked && !d.staleLocked(p) {
			return
		}
	}
	if !d.colorSettled {
		d.colorSettled = true
		if d.st.Color != 0 {
			d.kickPresence()
		}
	}
}

// colorHolderLocked is the peer that holds this device's picked colour instead of it, "" if none
// does. Caller holds mu.
func (d *Daemon) colorHolderLocked() string {
	mine := d.st.Color - 1
	if c := bus.DeviceColors(d.colorPicksLocked(d.st.Color, d.st.ColorAt), d.opt.StaleAfter); !c[d.device].Picked {
		for _, id := range sortedKeys(d.peers) {
			if c[id].Picked && c[id].Color == mine {
				return id
			}
		}
	}
	if !d.colorSettled {
		for _, id := range sortedKeys(d.peers) {
			p := d.peers[id]
			if p.pres.Color == d.st.Color && !bus.ColorGoneQuiet(peerPick(id, p), d.st.ColorAt, d.opt.StaleAfter) {
				return id
			}
		}
	}
	return ""
}

// colorAwayLocked notes that this device's presence on the remote is older than StaleAfter, as it
// is about to write a new one: its daemon was asleep or couldn't push, and to its peers it had
// gone quiet, so one of them may have taken its colour. Like a daemon that has just started, it
// leaves its pick out until the next full sync has settled it. Caller holds mu.
func (d *Daemon) colorAwayLocked(now time.Time) {
	if d.colorSettled && !d.presenceOut.IsZero() && now.Sub(d.presenceOut) > d.opt.StaleAfter {
		d.colorSettled = false
	}
}

// deviceCalledLocked is what the page calls device id: its nickname or person's name, else its
// owner, else its ID. Caller holds mu.
func (d *Daemon) deviceCalledLocked(id string) string {
	owner := ""
	if p := d.peers[id]; p != nil {
		owner = p.pres.Owner
	}
	return cmp.Or(d.nickOfLocked(id), owner, id)
}

// SetColor is the page picking this device's colour, 1 to bus.Colors, or with 0 letting it be
// chosen. A colour another device holds is refused, unless that device is stale here and its
// presence was written more than StaleAfter ago, so the pick takes it everywhere (#581). It waits
// for the first sync, as a nickname does: before it, this daemon knows no other device's colour.
func (s pageSource) SetColor(ctx context.Context, color int) (web.Account, error) {
	d := s.d
	d.mu.Lock()
	d.pageSeenLocked(ctx, time.Now())
	d.mu.Unlock()
	if color < 0 || color > bus.Colors {
		return web.Account{}, fmt.Errorf("there is no colour %d", color)
	}
	if color != 0 {
		d.awaitFirstSync(ctx)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if color != d.st.Color {
		now := time.Now().UTC()
		for id, c := range d.colorsLocked() {
			if id == d.device || !c.Picked || c.Color != color-1 {
				continue
			}
			// Both: a presence quiet by its own times can be a live device's whose clock runs
			// behind, and one stale here can have been written by a clock that runs ahead.
			if p := d.peers[id]; p == nil || !d.staleLocked(p) || !bus.ColorGoneQuiet(peerPick(id, p), now, d.opt.StaleAfter) {
				return web.Account{}, fmt.Errorf("%s has that colour", d.deviceCalledLocked(id))
			}
		}
		d.st.Color, d.st.ColorAt, d.st.ColorLostTo = color, time.Time{}, ""
		if color != 0 {
			d.st.ColorAt = now
		}
		d.saveLocked()
		d.kickPresence()
	} else if d.st.ColorLostTo != "" {
		d.st.ColorLostTo = "" // seen: picking Auto again takes the note away
		d.saveLocked()
	}
	return d.accountLocked(), nil
}
