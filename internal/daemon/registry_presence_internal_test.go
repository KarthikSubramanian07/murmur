package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
)

// A race that sank the first fix for #40 (Codex's review, 2026-10-29), as a test: a presence newer
// than the registry it was read with must not have a healthy registry rewritten.

// seePresence has d read a presence of dev's, from a daemon before encryption, that shows one
// session, id, called name. Caller holds advMu, as a sync does.
func seePresence(t *testing.T, d *Daemon, dev, id, name string) {
	t.Helper()
	ctx := context.Background()
	at := time.Now().UTC()
	p := bus.Presence{V: bus.Version, Device: dev, Host: "far", At: at,
		Sessions: []bus.SessionInfo{{ID: id, Name: name, Agent: "claude", Started: at}}}
	msg, err := bus.Encode(p, "presence")
	if err != nil {
		t.Fatal(err)
	}
	sha, err := d.repo.NewCommit(ctx, "", msg)
	if err != nil {
		t.Fatal(err)
	}
	d.updatePeer(ctx, brand.PresencePrefix+dev, sha)
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.st.Used[name]; !ok {
		t.Fatalf("%s, in %s's presence, wasn't taken for used", name, dev)
	}
}

// nameCommit makes a registry record on parent in d's clone: name, given to device/session.
func nameCommit(t *testing.T, d *Daemon, parent, name, device, session string) string {
	t.Helper()
	rec := bus.NameRecord{V: bus.Version, Name: name, Device: device, Session: session, At: time.Now().UTC()}
	msg, err := bus.Encode(rec, rec.Subject())
	if err != nil {
		t.Fatal(err)
	}
	sha, err := d.repo.NewCommit(context.Background(), parent, msg)
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

// Another device takes a name, and its presence shows it, after this daemon last read the
// registry: the presence is newer than what was read, and the name isn't lost. The next sync
// lists the registry with that name in it, and leaves it as it is.
func TestPresenceNewerThanTheRegistryReadIsNoLoss(t *testing.T) {
	t.Parallel()
	d, _ := namedDaemon(t)
	ctx := context.Background()
	theirs := freshNames(t, 81, 1)[0]
	tip := nameCommit(t, d, d.st.NamesTip, theirs, "far-0001", "ffffff")
	runGit(t, filepath.Dir(d.opt.GitDir), "push", "-q", "origin", tip+":"+brand.NamesRef)

	d.advMu.Lock()
	seePresence(t, d, "far-0001", "ffffff", theirs)
	d.processNames(ctx, tip) // the next sync, which lists the registry with the name
	d.advMu.Unlock()

	if d.st.NamesRepair {
		t.Error("a registry that holds every name was set to be repaired")
	}
	if d.st.NamesTip != tip || d.st.NamesGood != tip {
		t.Errorf("the registry's tip became %.7s, last read in full %.7s; want both %.7s", d.st.NamesTip, d.st.NamesGood, tip)
	}
	if w := d.nextNames(); !w.empty() {
		t.Errorf("the assigner has work to do: %+v", w)
	}
	if got, err := d.repo.RemoteRef(ctx, brand.NamesRef); err != nil || got != tip {
		t.Errorf("the registry on the remote is %.7s (%v); it should still be %.7s", got, err, tip)
	}
}
