package daemon_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/web"
)

// Tests of a device's colour (#573): its person picks it on the page, every teammate's page shows
// it, and a colour another device has can't be picked.

// colorOn is device id's colour as page p shows it.
func (p *page) colorOn(id string) (web.Device, bool) {
	for _, d := range p.state().Devices {
		if d.ID == id {
			return d, true
		}
	}
	return web.Device{}, false
}

func (p *page) pickColor(color int) (int, web.Account, string) {
	p.t.Helper()
	code, b := p.call("PUT", "/api/color", p.token, fmt.Sprintf(`{"color":%d}`, color))
	var acct web.Account
	json.Unmarshal([]byte(b), &acct)
	return code, acct, b
}

func TestAPickedColourIsSeenByTeammatesAndCantBeTaken(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	pa, pb := a.page(), b.page()
	eventually(t, "each to see the other", func() bool {
		_, ab := pa.colorOn(b.self())
		_, ba := pb.colorOn(a.self())
		return ab && ba
	})

	// a picks the colour b had chosen for it: b's steps aside, on both pages.
	bWas, _ := pa.colorOn(b.self())
	if bWas.Picked {
		t.Fatalf("b picked nothing, and shows %+v", bWas)
	}
	pick := bWas.Color + 1
	if code, acct, body := pa.pickColor(pick); code != http.StatusOK || acct.Color != pick {
		t.Fatalf("picking %d: %d %s", pick, code, body)
	}
	if me, _ := pa.colorOn(a.self()); me.Color != bWas.Color || !me.Picked || pa.state().Account.Color != pick {
		t.Errorf("a's own page shows it as %+v", me)
	}
	for name, p := range map[string]*page{"a": pa, "b": pb} {
		eventually(t, name+"'s page to show a's colour, and b's elsewhere", func() bool {
			ad, _ := p.colorOn(a.self())
			bd, _ := p.colorOn(b.self())
			return ad.Color == bWas.Color && ad.Picked && bd.Color != bWas.Color && !bd.Picked
		})
	}

	// b can't take it, and says whose it is.
	if code, _, body := pb.pickColor(pick); code != http.StatusBadRequest || !strings.Contains(body, a.self()+" has that colour") {
		t.Errorf("b taking a's colour: %d %s", code, body)
	}
	for _, bad := range []int{-1, 13} {
		if code, _, body := pb.pickColor(bad); code != http.StatusBadRequest {
			t.Errorf("colour %d: %d %s", bad, code, body)
		}
	}

	// Once a goes back to Auto, b may have it.
	if code, acct, body := pa.pickColor(0); code != http.StatusOK || acct.Color != 0 {
		t.Fatalf("going back to auto: %d %s", code, body)
	}
	eventually(t, "b to see a's colour let go", func() bool {
		ad, _ := pb.colorOn(a.self())
		return !ad.Picked
	})
	if code, _, body := pb.pickColor(pick); code != http.StatusOK {
		t.Errorf("b taking the colour a let go: %d %s", code, body)
	}
}
