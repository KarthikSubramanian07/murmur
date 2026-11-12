package daemon

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/names"
	"github.com/a-Fig/murmur/internal/proto"
)

// A device's nickname (#190). A device's ID is its hostname and four hex digits, which says which
// machine and not whose. Its user can give it a nickname, such as their own name; presence carries
// it, so every teammate sees the same one. A device with none is called by its person's name, if
// someone chose one on the page, so most devices never need one set. The ID stays the device's
// address on the bus and in its refs: a nickname stands in for it only in what a caller types, and
// is turned into the ID here. docs/VISION.md ("Device nicknames") has the reasons.

// nickOfLocked is what device dev is called: the nickname its user gave it, else its person's
// name. "" if it has neither. Caller holds mu.
func (d *Daemon) nickOfLocked(dev string) string {
	if dev == d.device {
		return cmp.Or(d.st.Nick, d.st.Person)
	}
	p := d.peers[dev]
	if p == nil {
		return ""
	}
	if p.pres.Nick != "" {
		return p.pres.Nick
	}
	for _, s := range p.pres.Sessions {
		if s.ID == bus.PersonSession {
			return s.Name
		}
	}
	return ""
}

// isDeviceLocked says id is the ID of a device this daemon knows. Caller holds mu.
func (d *Daemon) isDeviceLocked(id string) bool {
	_, peer := d.peers[id]
	return peer || id == d.device
}

// devicesCalledLocked lists the devices called nick, this one first. Caller holds mu.
func (d *Daemon) devicesCalledLocked(nick string) []string {
	var out []string
	if d.nickOfLocked(d.device) == nick {
		out = append(out, d.device)
	}
	for _, dev := range sortedKeys(d.peers) {
		if d.nickOfLocked(dev) == nick {
			out = append(out, dev)
		}
	}
	return out
}

// resolveNickLocked turns an address whose device is given by nickname into the device's own:
// tyler/bd0102 into laptop-3f2a/bd0102, and tyler alone into laptop-3f2a. A device's ID stands
// before a nickname, so no nickname can take a device's messages; an address that names no
// nickname comes back as it was. Nothing keeps two devices from one nickname (any device can say
// any in its presence), so when several have it the session decides, if only one of them knows
// it, and otherwise the address is refused rather than sent to a guess. Caller holds mu.
func (d *Daemon) resolveNickLocked(target string) (string, error) {
	nick, sess := bus.SplitAddr(target)
	if !bus.ValidNick(nick) || d.isDeviceLocked(nick) {
		return target, nil
	}
	devs := d.devicesCalledLocked(nick)
	if len(devs) > 1 && sess != "" {
		var has []string
		for _, dev := range devs {
			if d.sessionKnownLocked(dev, sess) {
				has = append(has, dev)
			}
		}
		if len(has) == 1 {
			devs = has
		}
	}
	switch len(devs) {
	case 0:
		return target, nil
	case 1:
		if sess == "" {
			return devs[0], nil
		}
		return bus.Addr(devs[0], sess), nil
	}
	return "", fmt.Errorf("%d devices are called %s: %s. Use the device's ID in place of %s.",
		len(devs), nick, strings.Join(devs, ", "), nick)
}

// nickProblemLocked says why nick can't be this device's nickname, "" if it can. A nickname
// another device has is no problem: see resolveNickLocked. Caller holds mu.
func (d *Daemon) nickProblemLocked(nick string) string {
	if !bus.ValidNick(nick) {
		return fmt.Sprintf("%q can't be a nickname. Use 2 to %d lowercase letters and digits, with single dashes between words, and not a session's name.", nick, names.MaxPerson)
	}
	if d.isDeviceLocked(nick) {
		return fmt.Sprintf("%s is a device's ID here, so an address with it means that device.", nick)
	}
	if addr, ok := d.resolvePersonLocked(nick); ok && addr != d.personAddr() {
		dev, _ := bus.SplitAddr(addr)
		return fmt.Sprintf("%s is the name of the person on %s.", nick, dev)
	}
	return ""
}

// nick is OpNick: it says what this device is called, gives it a nickname, or takes its nickname
// off.
func (d *Daemon) nick(req proto.Request) proto.Response {
	d.mu.Lock()
	defer d.mu.Unlock()
	nick := strings.TrimSpace(req.Nick)
	switch {
	case req.Clear:
		d.st.Nick = ""
	case nick != "":
		if why := d.nickProblemLocked(nick); why != "" {
			return fail(why)
		}
		d.st.Nick = nick
	}
	if req.Clear || nick != "" {
		d.saveLocked()
		d.kickPresence()
	}
	var note string
	switch called := d.nickOfLocked(d.device); {
	case d.st.Nick != "":
		note = fmt.Sprintf("This device, %s, has the nickname %s: teammates see it in who, and %s/<session> reaches a session on it.", d.device, called, called)
		var others []string
		for _, dev := range d.devicesCalledLocked(called) {
			if dev != d.device {
				others = append(others, dev)
			}
		}
		if len(others) > 0 {
			note += fmt.Sprintf(" %s is called %s too, so an address that could mean either is refused.", strings.Join(others, ", "), called)
		}
	case called != "":
		note = fmt.Sprintf("This device, %s, has no nickname. It is called %s, the name its person chose on %s's page.", d.device, called, brand.Product)
	default:
		note = fmt.Sprintf("This device, %s, has no nickname.", d.device)
	}
	return proto.Response{OK: true, Self: bus.Addr(d.device, proto.CLISession), Note: note}
}
