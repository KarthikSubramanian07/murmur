package daemon_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A device pushes one ref at a time. GitHub refused now and then one of the two or three pushes a
// device made in the same second, as a session started or stopped (#141). The remote's pre-receive
// hook here records any push that arrives while another is being received, and holds each for a
// second, so that pushes made together would meet in it.
func TestPushesNeverOverlap(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	script := "#!/bin/sh\n" +
		"if ! mkdir \"$GIT_DIR/inpush\" 2>/dev/null; then echo overlap >> \"$GIT_DIR/overlaps\"; fi\n" +
		"cat >/dev/null\n" +
		"sleep 1\n" +
		"rmdir \"$GIT_DIR/inpush\" 2>/dev/null\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	a := newDevice(t, remote)
	dev := a.self()

	// A session starting pushes its name, its start on the device's log, and the device's presence.
	for _, id := range []string{"aaaaaa", "aaaaab", "aaaaac"} {
		addr, _ := a.session(id, "claude-"+id)
		name := a.named(addr)
		eventually(t, "the registry to have "+name, func() bool { return readRegistry(t, remote).holdsNames(name) })
	}
	// The log's ref can still be missing here: a session's start waits for its presence, so the
	// registry can hold every name before the first start is pushed, and busText fails on a ref the
	// remote doesn't have.
	mesh := "refs/murmur/mesh/" + dev
	eventually(t, "the sessions' starts on the bus", func() bool {
		return git(t, remote, "for-each-ref", mesh) != "" && strings.Count(busText(t, remote, dev), " started (") == 3
	})
	if b, err := os.ReadFile(filepath.Join(remote, "overlaps")); err == nil {
		t.Fatalf("%d pushes arrived while another was being received", strings.Count(string(b), "overlap"))
	}
}

// Of the pushes a session's start sets off, the device's presence goes first: it is the one that
// shows the session in other devices' who. Its start on the log and its name's assignment were
// ready first and went first, and over GitHub, at more than a second a push, the presence waited
// behind both. The remote's pre-receive hook here records the order refs arrive in, and holds each
// push for a second, as a slow remote does.
func TestSessionStartPushesPresenceFirst(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	order := filepath.Join(remote, "order")
	script := "#!/bin/sh\n" +
		"while read old new ref; do echo \"$ref\" >> \"$GIT_DIR/order\"; done\n" +
		"sleep 1\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// A pool, so the session's name is the device's at once and the registry is told after; no
	// heartbeat to push presence in between.
	a := newDeviceWith(t, remote, func(d *device) { d.pool, d.beat = 2, time.Hour })
	dev := a.self()
	presence, mesh := "refs/murmur/presence/"+dev, "refs/murmur/mesh/"+dev
	eventually(t, "the device's presence and its names set aside", func() bool {
		return len(readRegistry(t, remote).records) >= 2 && git(t, remote, "for-each-ref", presence) != ""
	})
	if err := os.Remove(order); err != nil {
		t.Fatal(err)
	}

	addr, _ := a.session("aaaaaa", "claude-a")
	name := a.named(addr)
	eventually(t, "the session's start on its log", func() bool {
		return git(t, remote, "for-each-ref", mesh) != "" && strings.Contains(busText(t, remote, dev), " started (")
	})
	eventually(t, "the registry to have "+name+" for the session", func() bool {
		for _, rec := range readRegistry(t, remote).records {
			if rec.Name == name && rec.Session == "aaaaaa" {
				return true
			}
		}
		return false
	})
	b, err := os.ReadFile(order)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(b))
	if len(got) == 0 || got[0] != presence || !slices.Contains(got, mesh) || !slices.Contains(got, "refs/murmur/names") {
		t.Fatalf("pushes arrived in the order %v; want %s first, then %s and the registry", got, presence, mesh)
	}
}

// A presence push that fails still lets the rest of a session's start go: its start on the log and
// its name's assignment wait for the presence to have been tried, not for it to have landed. A gate
// opened only by a push that landed would leave the assigner waiting for good, with the pool
// never filled again, and drop every line appendSoon writes. The remote's pre-receive hook here
// refuses every presence push.
func TestARefusedPresenceDoesNotHoldBackTheStartOrTheName(t *testing.T) {
	t.Parallel()
	remote := newRemote(t)
	a := newDeviceWith(t, remote, func(d *device) { d.pool, d.beat = 2, time.Hour })
	dev := a.self()
	mesh := "refs/murmur/mesh/" + dev
	eventually(t, "the device's presence and its names set aside", func() bool {
		return len(readRegistry(t, remote).records) >= 2 && git(t, remote, "for-each-ref", "refs/murmur/presence/"+dev) != ""
	})
	script := "#!/bin/sh\n" +
		"while read old new ref; do case \"$ref\" in refs/murmur/presence/*) exit 1;; esac; done\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	addr, _ := a.session("aaaaaa", "claude-a")
	name := a.named(addr)
	eventually(t, "the session's start on its log and "+name+" in the registry", func() bool {
		if git(t, remote, "for-each-ref", mesh) == "" || !strings.Contains(busText(t, remote, dev), " started (") {
			return false
		}
		for _, rec := range readRegistry(t, remote).records {
			if rec.Name == name && rec.Session == "aaaaaa" {
				return true
			}
		}
		return false
	})
}
