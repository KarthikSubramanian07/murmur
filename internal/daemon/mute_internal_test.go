package daemon

import (
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
)

// A mute holds against a message to everyone or to the device from the first moment, and against
// one sent to the muting session itself once MuteGrace has passed: until then the sender's daemon
// may not have read of the mute, and would take its message for delivered (#466).
func TestAMuteHoldsAgainstDirectMessagesAfterItsGrace(t *testing.T) {
	const me, muter, sender = "here-0001", "quiet-otter", "brave-heron"
	from := bus.Addr("there-0002", "aaaaaa")
	daemonAt := func(muted time.Duration) *Daemon {
		return &Daemon{device: me, opt: Options{MuteGrace: 30 * time.Second},
			sessions: map[string]*session{"bbbbbb": {id: "bbbbbb", name: muter}},
			st: state{Used: map[string]string{sender: from},
				Mutes: map[string]map[string]time.Time{muter: {sender: time.Now().Add(-muted)}}}}
	}
	direct := bus.Event{From: from, To: bus.Addr(me, "bbbbbb")}
	for _, c := range []struct {
		what  string
		ago   time.Duration
		e     bus.Event
		muted bool
	}{
		{"a direct message, just after the mute", time.Second, direct, false},
		{"a direct message, after the grace", time.Minute, direct, true},
		{"a message to everyone, just after the mute", time.Second, bus.Event{From: from, To: "*"}, true},
		{"a message to the device, just after the mute", time.Second, bus.Event{From: from, To: me}, true},
		{"a direct message from someone else, after the grace", time.Minute, bus.Event{From: bus.Addr("there-0002", "cccccc"), To: bus.Addr(me, "bbbbbb")}, false},
	} {
		if got := daemonAt(c.ago).mutedHereLocked("bbbbbb", c.e); got != c.muted {
			t.Errorf("%s: dropped %v, want %v", c.what, got, c.muted)
		}
	}
	// With no grace, as the other tests run, a direct message is dropped at once.
	d := daemonAt(time.Second)
	d.opt.MuteGrace = -1
	if !d.mutedHereLocked("bbbbbb", direct) {
		t.Error("with no grace, a direct message got through")
	}
}
