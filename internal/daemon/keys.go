package daemon

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/gitx"
)

// The bus is encrypted (#156): every record with content goes out encrypted for the team's devices,
// with nothing for anyone to run, approve or copy. Each device makes its own keys at start, publishes
// the public halves in its sealed presence, and pins its teammates' keys the first time it reads
// them. Push access is the key directory: only collaborators can write refs/murmur/*, so only they
// can publish a key. docs/VISION.md, "The bus is encrypted", has the reasons and what it costs.

// keyPin is what this device knows of a device's keys: the signing key it pinned on first sight,
// the box key that device's latest sealed presence carried, and when this daemon last took a sealed
// presence from it that it hadn't taken before (SHA), by this machine's clock (see noteSeenLocked).
type keyPin struct {
	Sign string    `json:"sign"`
	Box  string    `json:"box"`
	Seen time.Time `json:"seen,omitzero"`
	SHA  string    `json:"sha,omitempty"`
}

// keyFile is the device's private keys, in .git/murmur/keys: never committed, never on the bus.
type keyFile struct {
	Sign string `json:"sign"` // Ed25519 seed, hex
	Box  string `json:"box"`  // X25519 private key, hex
}

// loadIdentity reads this clone's device ID and keys, creating what is missing. The keys are kept
// next to the device ID, in a file of their own, so a daemon from before encryption, which reads
// only the device file, still finds its ID.
//
// A device that has published keys is known to its teammates by them, and they refuse any other
// under its ID. So a device whose keys are lost takes a new ID with new keys: to its teammates it is
// a new device, rather than one whose presence they refuse. It had keys if its state pinned them,
// or, should state.json be gone too, if the clone's copy of its own presence is sealed (sealed, as
// git reads it from that copy). A device that never had keys, a daemon from before encryption,
// keeps its ID.
func loadIdentity(dir string, lg *log.Logger, sealed func(device string) bool) (string, ed25519.PrivateKey, *ecdh.PrivateKey, error) {
	device, err := loadDevice(dir)
	if err != nil {
		return "", nil, nil, err
	}
	path := filepath.Join(dir, "keys")
	b, err := os.ReadFile(path)
	if err == nil {
		if sign, box, ok := parseKeys(b); ok {
			return device, sign, box, nil
		}
		if lg != nil {
			lg.Printf("%s is unreadable; making new keys", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", nil, nil, err
	}
	if hadKeys(filepath.Join(dir, "state.json"), device) || sealed != nil && sealed(device) {
		if err := os.Remove(filepath.Join(dir, "device")); err != nil {
			return "", nil, nil, err
		}
		old := device
		if device, err = loadDevice(dir); err != nil {
			return "", nil, nil, err
		}
		if lg != nil {
			lg.Printf("the keys of %s are gone; this clone is device %s from now on", old, device)
		}
	}
	sign, box := bus.NewSignKey(), bus.NewBoxKey()
	out, err := json.Marshal(keyFile{Sign: hex.EncodeToString(sign.Seed()), Box: hex.EncodeToString(box.Bytes())})
	if err != nil {
		return "", nil, nil, err
	}
	if err := writeFileAtomic(path, append(out, '\n')); err != nil {
		return "", nil, nil, err
	}
	return device, sign, box, nil
}

func parseKeys(b []byte) (ed25519.PrivateKey, *ecdh.PrivateKey, bool) {
	var kf keyFile
	if json.Unmarshal(b, &kf) != nil {
		return nil, nil, false
	}
	seed, err := hex.DecodeString(kf.Sign)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, nil, false
	}
	raw, err := hex.DecodeString(kf.Box)
	if err != nil {
		return nil, nil, false
	}
	box, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return nil, nil, false
	}
	return ed25519.NewKeyFromSeed(seed), box, true
}

// writeFileAtomic writes a private file whole or not at all.
func writeFileAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// hadKeys says whether the state file records keys for device, which it does once the device has
// run with them.
func hadKeys(path, device string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var st struct {
		Pins map[string]*keyPin `json:"pins"`
	}
	return json.Unmarshal(b, &st) == nil && st.Pins[device] != nil
}

// sealedLocally says whether the clone's local copy of device's presence ref is sealed by device:
// it published keys. A ref that isn't there, or can't be read, says no.
func sealedLocally(ctx context.Context, g gitx.Git) func(string) bool {
	return func(device string) bool {
		ref := brand.PresencePrefix + device
		out, err := g.Run(ctx, "", "log", "-1", "--no-show-signature", "--no-color", "--format=%B", ref, "--")
		if err != nil {
			return false
		}
		st, seal := bus.OpenSeal(out, ref)
		return st == bus.Sealed && seal.By == device
	}
}

// pinSelfLocked records this device's own keys among the pins, which is what tells a later start
// that keys gone missing were this device's (see loadIdentity). Caller holds mu.
func (d *Daemon) pinSelfLocked() {
	pin := &keyPin{Sign: bus.SignPublic(d.signKey), Box: bus.BoxPublic(d.boxKey), Seen: time.Now().UTC()}
	if old := d.st.Pins[d.device]; old == nil || old.Sign != pin.Sign || old.Box != pin.Box {
		d.st.Pins[d.device] = pin
		d.saveLocked()
	}
}

// checkPresenceLocked decides whether a presence read from dev's ref is to be taken as dev's, and
// pins dev's keys from it. The first sealed presence read for a device pins its signing key, and
// from then on only presence sealed with that key is: none unsealed, so a seal can't be stripped
// off, and none with another key, so a pin can't be undone by the next writer. Until then an
// unsealed presence is taken as it always was, since daemons from before encryption write nothing
// else. A refusal is logged, at most once a minute for each device, and nobody is asked anything.
//
// This trusts whoever seals first. Anyone who can push could seal a presence for a device before a
// reader has seen the device's own, and that reader would then refuse the device: what a team of
// people who can all push already trust each other with. Caller holds mu.
func (d *Daemon) checkPresenceLocked(dev, ref string, c bus.Commit, pres bus.Presence) (ok, sealed bool) {
	state, seal := bus.OpenSeal(c.Message, ref)
	pin := d.st.Pins[dev]
	switch {
	case state == bus.Unsealed && pin == nil && pres.BoxKey == "" && pres.Box == nil:
		return true, false // a daemon from before encryption
	case state == bus.Sealed && seal.By == dev && (pin == nil || pin.Sign == seal.Key) && bus.ValidBoxKey(pres.BoxKey):
		switch {
		case pin == nil:
			d.st.Pins[dev] = &keyPin{Sign: seal.Key, Box: pres.BoxKey}
			d.saveLocked()
		case pin.Box != pres.BoxKey:
			pin.Box = pres.BoxKey
			d.saveLocked()
		}
		return true, true
	}
	why := "its seal doesn't hold"
	switch {
	case state == bus.Unsealed && pin != nil:
		why = "it has no seal, and " + dev + " seals its presence"
	case state == bus.Unsealed:
		why = "it carries a key but no seal"
	case state == bus.Sealed && seal.By != dev:
		why = "it is sealed by " + seal.By
	case state == bus.Sealed && pin != nil && pin.Sign != seal.Key:
		why = "it is sealed with a key that isn't the one pinned for " + dev
	case state == bus.Sealed:
		why = "its box key isn't a usable key"
	}
	if t, ok := d.refused[dev]; !ok || time.Since(t) > time.Minute {
		d.log.Printf("%s: ignoring %s, which claims to be from %s: %s", ref, short(c.SHA), dev, why)
	}
	d.refused[dev] = time.Now()
	return false, false
}

// clockAllowance is how far ahead of this machine's clock a writer's may run before what it dates
// is taken for no date at all.
const clockAllowance = time.Minute

// firstVerified is when to take a sealed presence first read in this run as written: by its
// writer's clock, as firstSight does, unless that clock is ahead of this one by more than
// clockAllowance. A presence dated in the future would otherwise look current, for as long as its
// writer chose, to a daemon that starts any time before then, and a device whose user has lost push
// access could go on reading what is new that way (Sol's review of #156). Such a presence counts
// as never verified, until the device writes again and this daemon sees it change.
func firstVerified(written time.Time) time.Time {
	if written.After(time.Now().Add(clockAllowance)) {
		return time.Time{}
	}
	return firstSight(written)
}

// noteSeenLocked records that this daemon took sha, a sealed presence of dev's, verified when
// verified says (on this machine's clock), for the 24 hours a message addressed to dev may still be
// encrypted for it (encryptEventLocked). Reading the same presence again, as after a restart,
// renews nothing: only a presence this daemon hasn't taken before does. It is saved with the next
// save, as a presence comes every 2 minutes. Caller holds mu.
func (d *Daemon) noteSeenLocked(dev, sha string, verified time.Time) {
	pin := d.st.Pins[dev]
	if pin == nil || verified.IsZero() || pin.SHA == sha {
		return
	}
	pin.SHA = sha
	if wall := time.Now().Add(-time.Since(verified)).Round(0).UTC(); wall.After(pin.Seen) {
		pin.Seen = wall
	}
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// teamKeysLocked is who this device's records are encrypted for: itself, and every other device
// whose keys are pinned and from which this daemon took a sealed presence within StaleAfter. That
// is how a collaborator removed from the repo, whose daemon can no longer push, stops getting what
// is new within StaleAfter.
//
// It is measured against now, not against the last successful sync as staleLocked is for showing a
// device: a daemon that has been offline knows nothing of who is on the team now, so after
// StaleAfter it writes for itself and the device a message is addressed to, until a sync shows it
// the team again. Nor does a presence that couldn't be read, or that was refused, count: anyone who
// can push could otherwise keep a device that has gone in the team by pushing junk over its
// presence (Sol's review of #156). Caller holds mu.
func (d *Daemon) teamKeysLocked() map[string]string {
	keys := map[string]string{d.device: bus.BoxPublic(d.boxKey)}
	for dev, p := range d.peers {
		pin := d.st.Pins[dev]
		if pin != nil && pin.Box != "" && !p.verified.IsZero() && time.Since(p.verified) <= d.opt.StaleAfter {
			keys[dev] = pin.Box
		}
	}
	return keys
}

// teamKeyID is a set of keys as one string, to tell whether it changed.
func teamKeyID(keys map[string]string) string {
	ids := make([]string, 0, len(keys))
	for dev, k := range keys {
		ids = append(ids, dev+"="+k)
	}
	sort.Strings(ids)
	return strings.Join(ids, " ")
}

// errOldDevice says a device can't read what this one writes.
func errOldDevice(dev string) error {
	return fmt.Errorf("%s runs a version of Murmur from before the bus was encrypted, so it couldn't read this. "+
		"It can once it is updated: Murmur updates itself within the hour, unless updates are turned off there", dev)
}

// encryptEventLocked returns e as this device writes it: encrypted for the team (teamKeysLocked),
// and for the device it is addressed to, if any. A device addressed directly gets it even while its
// presence is stale, for a kept inbox there (#30), as long as it was seen within StoppedInbox. A
// message to a device whose keys aren't known, or that hasn't been seen for longer, is refused:
// nothing goes out in the clear. Caller holds mu.
func (d *Daemon) encryptEventLocked(e bus.Event) (bus.Event, error) {
	keys := d.teamKeysLocked()
	if e.To != "" && e.To != "*" {
		dev, _ := bus.SplitAddr(e.To)
		if _, ok := keys[dev]; !ok {
			pin := d.st.Pins[dev]
			switch {
			case pin != nil && pin.Box != "" && time.Since(pin.Seen) <= d.opt.StoppedInbox:
				keys[dev] = pin.Box
			case e.Type != bus.Message:
				// A human's tags for a session on a device that can't read them: the others still
				// should, so it goes out without that device.
			case pin == nil || pin.Box == "":
				if p := d.peers[dev]; p != nil {
					return bus.Event{}, errOldDevice(dev)
				}
				return bus.Event{}, fmt.Errorf("%s isn't a device this one has seen, so there is no key to encrypt to", dev)
			default:
				return bus.Event{}, fmt.Errorf("%s hasn't been seen for more than %s, and messages are encrypted only for the devices that are around",
					dev, d.opt.StoppedInbox)
			}
		}
	}
	return bus.EncryptEvent(e, d.device, keys)
}

// openEventLocked decrypts an event read from dev's log, if it is encrypted. ok is false for one
// this device can't read: written before it published its keys, or while its writer didn't count
// it among the team's current devices, or one that doesn't open. Caller holds mu.
func (d *Daemon) openEventLocked(e bus.Event, dev, ref, sha string) (bus.Event, bool) {
	if e.Type != bus.Boxed {
		return e, true
	}
	in, err := bus.DecryptEvent(e, dev, d.device, d.boxKey)
	if err != nil {
		if !errors.Is(err, bus.ErrNotForUs) {
			d.log.Printf("%s: ignoring %s: %v", ref, short(sha), err)
		}
		return bus.Event{}, false
	}
	return in, true
}
