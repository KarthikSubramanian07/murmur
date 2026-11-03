package daemon

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
)

// loadIdentity reads this clone's device ID and signing key, creating what is missing. The key
// is kept next to the device ID, in a file of its own, so a daemon from before seals, which reads
// only the device file, still finds its ID.
//
// A device that has sealed records is known to its peers by that key alone, and they refuse any
// other under its ID. So a device whose key is lost, while its state shows it had one, takes a new
// ID with the new key: to its peers it is a new device, rather than one they would never hear
// from again. A device that never had a key, a daemon from before seals, keeps its ID.
func loadIdentity(dir string, lg *log.Logger) (string, ed25519.PrivateKey, error) {
	device, err := loadDevice(dir)
	if err != nil {
		return "", nil, err
	}
	keyPath := filepath.Join(dir, "key")
	if b, err := os.ReadFile(keyPath); err == nil {
		if seed, err := hex.DecodeString(strings.TrimSpace(string(b))); err == nil && len(seed) == ed25519.SeedSize {
			return device, ed25519.NewKeyFromSeed(seed), nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	if hadKey(filepath.Join(dir, "state.json"), device) {
		if err := os.Remove(filepath.Join(dir, "device")); err != nil {
			return "", nil, err
		}
		old := device
		if device, err = loadDevice(dir); err != nil {
			return "", nil, err
		}
		if lg != nil {
			lg.Printf("the signing key of %s is gone; this clone is device %s from now on", old, device)
		}
	}
	key := bus.NewKey()
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(key.Seed())+"\n"), 0o600); err != nil {
		return "", nil, err
	}
	return device, key, nil
}

// hadKey says whether the state file records a key for device, which it does once the device has
// run with one.
func hadKey(path, device string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var st struct {
		Keys map[string]string `json:"keys"`
	}
	return json.Unmarshal(b, &st) == nil && st.Keys[device] != ""
}

// sealOKLocked decides whether a record read from ref, which says device dev wrote it, is to be
// taken as dev's (#155). The first sealed record read for a device pins its key, and from then on
// only records sealed with that key are: none unsealed, so a seal can't be stripped off, and none
// with another key, so pinning can't be undone by the next writer. Until then an unsealed record is
// taken as it always was, since daemons from before seals write nothing else; the page shows such a
// device as unverified.
//
// This trusts whoever seals first. Anyone who can push could seal a record for a device before the
// device itself writes one, and its records would then be refused here: what a team of people who
// can all push already trust each other with. Caller holds mu.
func (d *Daemon) sealOKLocked(dev, ref string, c bus.Commit) bool {
	state, seal := bus.Open(c.Message, ref, c.Parents)
	pin := d.st.Keys[dev]
	switch {
	case state == bus.Unsealed && pin == "":
		d.unsealed[dev] = true
		return true
	case state == bus.Sealed && seal.By == dev && (pin == "" || pin == seal.Key):
		if pin == "" {
			d.st.Keys[dev] = seal.Key
			d.saveLocked()
		}
		delete(d.unsealed, dev)
		return true
	}
	why := "its seal doesn't hold"
	switch {
	case state == bus.Unsealed:
		why = "it has no seal, and " + dev + " seals what it writes"
	case state == bus.Sealed && seal.By != dev:
		why = "it is sealed by " + seal.By
	case state == bus.Sealed:
		why = "it is sealed with a key that isn't " + dev + "'s"
	}
	if t, ok := d.forged[dev]; !ok || time.Since(t) > time.Minute {
		d.log.Printf("%s: ignoring %s, which claims to be from %s: %s", ref, short(c.SHA), dev, why)
	}
	d.forged[dev] = time.Now()
	return false
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// nameSealOK decides whether a commit of the name registry is to be read. The registry is one
// ref every device writes, so a record is checked against the device that sealed it, and that
// device may record names only for itself, but in a new root: a daemon repairing the registry
// writes every device's reservations into it again (see pushNames). An unsealed record is
// checked against the device it names, so that one from a device that seals is refused. An
// unsealed root, or a snapshot naming no device, is read as before: daemons from before seals
// write them, and the names a snapshot lists only ever add to those known to be used. Caller
// doesn't hold mu.
func (d *Daemon) nameSealOK(rec bus.NameRecord, c bus.Commit) bool {
	state, seal := bus.Open(c.Message, brand.NamesRef, c.Parents)
	d.mu.Lock()
	defer d.mu.Unlock()
	switch state {
	case bus.Sealed:
		if !d.sealOKLocked(seal.By, brand.NamesRef, c) {
			return false
		}
		if rec.Name != "" && rec.Device != seal.By && c.Parents != "" {
			d.log.Printf("%s: ignoring %s, in which %s records a name for %s", brand.NamesRef, short(c.SHA), seal.By, rec.Device)
			return false
		}
		return true
	case bus.BadSeal:
		return d.sealOKLocked(rec.Device, brand.NamesRef, c) // false, and says so
	}
	if c.Parents == "" || rec.Name == "" || !bus.ValidDevice(rec.Device) {
		return true
	}
	return d.sealOKLocked(rec.Device, brand.NamesRef, c)
}
