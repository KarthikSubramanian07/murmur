package daemon

import (
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

// colorsLocked is every device's colour, this one's included, by the devices in presence. A
// device whose presence is stale still holds its colour: staleness is each reader's own clock,
// and the colours must come out the same everywhere. Caller holds mu.
func (d *Daemon) colorsLocked() map[string]bus.DeviceColor {
	picks := []bus.ColorPick{{Device: d.device, Color: d.st.Color, At: d.st.ColorAt}}
	for id, p := range d.peers {
		picks = append(picks, bus.ColorPick{Device: id, Color: p.pres.Color, At: p.pres.ColorAt})
	}
	return bus.DeviceColors(picks)
}

// SetColor is the page picking this device's colour, 1 to bus.Colors, or with 0 letting it be
// chosen. A colour another device holds is refused. It waits for the first sync, as a nickname
// does: before it, this daemon knows no other device's colour.
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
		for id, c := range d.colorsLocked() {
			if id != d.device && c.Picked && c.Color == color-1 {
				return web.Account{}, fmt.Errorf("%s has that colour", nameOr(d.nickOfLocked(id), id))
			}
		}
		d.st.Color, d.st.ColorAt = color, time.Time{}
		if color != 0 {
			d.st.ColorAt = time.Now().UTC()
		}
		d.saveLocked()
		d.kickPresence()
	}
	return d.accountLocked(), nil
}
