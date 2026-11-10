package daemon_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/proto"
)

// Tests of the names a device sets aside for sessions to come (#141): a new session takes one at
// once, without the remote, and the registry is told which session took it after.

// setAside lists the names the registry sets aside for device dev, oldest first.
func (r registry) setAside(dev string) []string {
	var got []string
	for _, rec := range r.records {
		if name, d, ok := rec.Reservation(); ok && d == dev {
			got = append(got, name)
		}
	}
	return got
}

// holders maps each name the registry assigns to a session to that session's address.
func (r registry) holders() map[string]string {
	got := map[string]string{}
	for _, rec := range r.records {
		if rec.Session != "" && rec.Name != "" {
			got[rec.Name] = bus.Addr(rec.Device, rec.Session)
		}
	}
	return got
}

// free lists the names set aside for dev that no session has in the registry yet.
func (r registry) free(dev string) []string {
	held := r.holders()
	var got []string
	for _, name := range r.setAside(dev) {
		if held[name] == "" {
			got = append(got, name)
		}
	}
	return got
}

// noNameSetAsideTwice fails the test if the registry sets a name aside, or assigns one, twice.
func noNameSetAsideTwice(t *testing.T, reg registry) {
	t.Helper()
	aside, assigned := map[string]bool{}, map[string]bool{}
	for _, rec := range reg.records {
		seen := assigned
		if _, _, ok := rec.Reservation(); ok {
			seen = aside
		} else if rec.Name == "" {
			continue
		}
		if seen[rec.Name] {
			t.Fatalf("the registry has %s twice:\n%s", rec.Name, reg.text)
		}
		seen[rec.Name] = true
	}
}

// A device sets names aside as soon as it starts. A session that starts while the remote is out of
// reach has one of them at once, in the hello's answer; when the remote is back, the registry is
// told which session took it, and the pool is filled again.
func TestSessionTakesANameSetAsideWithoutTheRemote(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a := newDeviceWith(t, remote, func(d *device) { d.pool = 2 })
	dev := a.self()
	var pool []string
	eventually(t, "two names set aside", func() bool {
		pool = readRegistry(t, remote).free(dev)
		return len(pool) == 2
	})

	a.offline(remote, true)
	conn, resp, err := a.c.Hello(context.Background(), proto.Hello{ID: "aaaaaa", Agent: "test", ClaudeSession: "claude-alice"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if resp.Name != pool[0] || resp.NamePending {
		t.Fatalf("the hello answers name %q, pending %v; want %s, the oldest set aside", resp.Name, resp.NamePending, pool[0])
	}
	if got := a.nameOf(resp.Self); got != pool[0] {
		t.Fatalf("who shows %q for the session", got)
	}

	a.offline(remote, false)
	eventually(t, "the registry to have the session down for its name, and two names free again", func() bool {
		reg := readRegistry(t, remote)
		free := reg.free(dev)
		return reg.holders()[pool[0]] == resp.Self && len(free) == 2 && free[0] == pool[1]
	})
	noNameSetAsideTwice(t, readRegistry(t, remote))
	holds(t, "two names free, and no more", time.Second, func() bool { return len(readRegistry(t, remote).free(dev)) == 2 })
}

// A name set aside for one device is never another's to give, even when both draw the same names:
// a daemon reads it as a name in use, as one that predates the pool does (see bus.NameRecord).
func TestNamesSetAsideAreNeverHandedOut(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a := newDeviceWith(t, remote, func(d *device) { d.pool = 2; d.names = seededNames(7) })
	dev := a.self()
	eventually(t, "two names set aside", func() bool { return len(readRegistry(t, remote).free(dev)) == 2 })
	first, second := nthName(t, 7, 1), nthName(t, 7, 2)
	if got := readRegistry(t, remote).free(dev); got[0] != first || got[1] != second {
		t.Fatalf("set aside %q, want %s and %s", got, first, second)
	}

	b := newNamedDevice(t, remote, seededNames(7)) // the same draws, and no pool of its own
	bob, _ := b.session("bbbbbb", "claude-bob")
	nb := b.named(bob)
	if nb == first || nb == second {
		t.Fatalf("bob is named %s, which is set aside for %s", nb, dev)
	}
	noNameSetAsideTwice(t, readRegistry(t, remote))
}

// A name set aside that no session has taken is in use, but nobody can be sent to: send says so,
// and says for which device, rather than that nobody has the name.
func TestSendToANameSetAside(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a := newDeviceWith(t, remote, func(d *device) { d.pool = 1 })
	b := newDevice(t, remote)
	dev := a.self()
	var aside string
	eventually(t, "a name set aside", func() bool {
		if free := readRegistry(t, remote).free(dev); len(free) == 1 {
			aside = free[0]
		}
		return aside != ""
	})
	b.session("bbbbbb", "claude-bob")
	eventually(t, "b to read it", func() bool {
		r := b.do(proto.Request{Op: proto.OpSend, Session: "bbbbbb", To: aside, Subject: "too early"})
		return strings.Contains(r.Error, "set it aside") && strings.Contains(r.Error, dev)
	})
	r := b.do(proto.Request{Op: proto.OpLookup, Session: "bbbbbb", Name: aside})
	if !strings.Contains(r.Error, "set it aside") {
		t.Fatalf("lookup of a name set aside: %+v", r)
	}

	// On its own device, the session that takes it is reached by it at once.
	alice, _ := a.session("aaaaaa", "claude-alice")
	if got := a.nameOf(alice); got != aside {
		t.Fatalf("alice is named %q, not %s", got, aside)
	}
	if r := a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: aside, Subject: "to myself"}); r.Error != "" {
		t.Fatalf("send to alice by name on her device: %+v", r)
	}
	eventually(t, "b to reach alice by name", func() bool {
		r := b.do(proto.Request{Op: proto.OpSend, Session: "bbbbbb", To: aside, Subject: "hello"})
		return r.Error == "" && len(r.Messages) == 1 && r.Messages[0].To == alice
	})
}

// A device's first start with the remote out of reach has no names set aside: its first session
// is pending, at device/session, until the remote is back, as before the pool.
func TestFirstStartOfflineIsPending(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a := newDeviceWith(t, remote, func(d *device) {
		d.pool = 2
		d.offline(remote, true)
	})
	alice, _ := a.session("aaaaaa", "claude-alice")
	r := a.do(proto.Request{Op: proto.OpWho, Session: "aaaaaa"})
	if r.Name != "" || !r.NamePending || r.Self != alice {
		t.Fatalf("who answers name %q, pending %v, self %q", r.Name, r.NamePending, r.Self)
	}
	a.offline(remote, false)
	name := a.named(alice)
	eventually(t, "the registry to have alice, and two names set aside", func() bool {
		reg := readRegistry(t, remote)
		return reg.holders()[name] == alice && len(reg.free(a.self())) == 2
	})
}
