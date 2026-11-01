package daemon

import (
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/web"
)

// What keeps a daemon with no sessions up, and how often it looks at the remote then (#87).

func idleDaemon(stoppedFor map[string]time.Duration) *Daemon {
	d := &Daemon{opt: Options{Poll: 3 * time.Second, QuietPoll: 30 * time.Second, PageOpen: 2 * time.Minute,
		StoppedInbox: time.Hour}, sessions: map[string]*session{},
		st: state{Stopped: map[string]time.Time{}, T3Routes: map[string]t3Route{}, SessionNames: map[string]string{}}}
	for id, ago := range stoppedFor {
		d.st.Stopped[id] = time.Now().Add(-ago)
		d.st.T3Routes[id] = t3Route{CLI: "/t3", Thread: "thread-" + id}
		d.st.SessionNames[id] = "name-" + id
	}
	return d
}

// A thread the daemon can still wake keeps it up: its session stopped within StoppedInbox, and the
// page's wake setting lets it be woken. A route whose inbox is no longer kept, a route of a session
// that went down with a daemon rather than stopping, and a thread the page says never to wake do
// not.
func TestHoldsRouteLocked(t *testing.T) {
	always := pageSettings{}
	never := pageSettings{Wake: web.Wake{When: web.WakeNever}}
	neverButOne := pageSettings{Wake: web.Wake{When: web.WakeNever},
		SessionWake: map[string]web.Wake{"name-aaaaaa": {When: web.WakeAlways}}}
	for _, tc := range []struct {
		name string
		d    func() *Daemon
		wake pageSettings
		want bool
	}{
		{"a thread stopped lately", func() *Daemon { return idleDaemon(map[string]time.Duration{"aaaaaa": time.Minute}) }, always, true},
		{"a thread stopped long ago", func() *Daemon { return idleDaemon(map[string]time.Duration{"aaaaaa": 2 * time.Hour}) }, always, false},
		{"no route at all", func() *Daemon { return idleDaemon(nil) }, always, false},
		{"a route that did not stop", func() *Daemon {
			d := idleDaemon(map[string]time.Duration{"aaaaaa": time.Minute})
			delete(d.st.Stopped, "aaaaaa")
			return d
		}, always, false},
		{"the page says never", func() *Daemon { return idleDaemon(map[string]time.Duration{"aaaaaa": time.Minute}) }, never, false},
		{"never, but not for this thread", func() *Daemon { return idleDaemon(map[string]time.Duration{"aaaaaa": time.Minute}) }, neverButOne, true},
		{"never, and another thread is the exception", func() *Daemon { return idleDaemon(map[string]time.Duration{"bbbbbb": time.Minute}) }, neverButOne, false},
		{"one of two", func() *Daemon {
			return idleDaemon(map[string]time.Duration{"aaaaaa": 2 * time.Hour, "bbbbbb": time.Minute})
		}, always, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.d()
			d.mu.Lock()
			defer d.mu.Unlock()
			if got := d.holdsRouteLocked(tc.wake); got != tc.want {
				t.Errorf("holdsRouteLocked = %v, want %v", got, tc.want)
			}
		})
	}
}

// The poll is QuietPoll while no session is live and no page is open, never faster than Poll; with
// one, Poll, or slower while nothing is said (see poll_rate_internal_test.go).
func TestPollBase(t *testing.T) {
	d := idleDaemon(nil)
	d.opt.PollCap = 15 * time.Second
	if got := d.pollEvery(3 * time.Second).every; got != 30*time.Second {
		t.Errorf("with nobody there: %v, want 30s", got)
	}
	d.lastPage = time.Now().Add(-time.Minute)
	if got := d.pollEvery(3 * time.Second).every; got != 6*time.Second {
		t.Errorf("with a page open: %v, want 6s, the first step of the slow phase", got)
	}
	d.lastPage = time.Now().Add(-3 * time.Minute)
	if got := d.pollEvery(3 * time.Second).every; got != 30*time.Second {
		t.Errorf("with a page closed: %v, want 30s", got)
	}
	d.sessions["aaaaaa"] = &session{id: "aaaaaa"}
	d.noteConversationLocked()
	d.opt.FastFor = time.Minute
	if got := d.pollEvery(15 * time.Second).every; got != 3*time.Second {
		t.Errorf("with a session and a conversation just moved: %v, want 3s", got)
	}
	d.sessions = map[string]*session{}
	d.opt.QuietPoll = time.Second
	if got := d.pollEvery(3 * time.Second).every; got != 3*time.Second {
		t.Errorf("with QuietPoll under Poll: %v, want Poll, 3s", got)
	}
}
