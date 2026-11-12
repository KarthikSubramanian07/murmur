package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/names"
	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/web"
)

// Mutes (#27, docs/VISION.md, "Mutes"). A session mutes another by its name, and from then on
// gets nothing the other sends: not a message to it, to its device, or to everyone. Its own device
// decides, by not queueing the message, and publishes the mutes in presence, so they are on the
// bus for anyone on the team to see, a human on the page among them. The muted session isn't
// told when it happens. It learns of it when it next sends to the session that muted it: its own
// daemon, which has read that presence, refuses the send and says why. That is the tool's answer,
// from Murmur and no agent, so it starts no turn anywhere.
//
// Mutes are kept as tags are, by the muting session's name, which stays the same while its
// conversation does, and name the muted session by its name, which the registry never gives out
// twice: a mute can't land on another session.

// mutesOfLocked is the names session id of this device has muted, in order. Caller holds mu.
func (d *Daemon) mutesOfLocked(id string) []string {
	return sortedKeys(d.st.Mutes[d.tagKeyLocked(id)])
}

// adoptMutesLocked moves the mutes a session made while its name was pending to the name, once
// it lands, as adoptTagsLocked does its tags. Caller holds mu.
func (d *Daemon) adoptMutesLocked(s *session) {
	pending := tagKeyOf("", s.id)
	m := d.st.Mutes[pending]
	if s.name == "" || m == nil {
		return
	}
	delete(d.st.Mutes, pending)
	if d.st.Mutes[s.name] == nil {
		d.st.Mutes[s.name] = m
	}
	d.saveLocked()
}

// nameOfLocked is the name of the session at addr: the one it has now, else the one the registry
// gave that address. "" for an address with none, as the CLI's or a person's. Caller holds mu.
func (d *Daemon) nameOfLocked(addr string) string {
	if bus.IsPerson(addr) {
		return ""
	}
	if name, _, _ := d.sessionInfoLocked(addr); name != "" {
		return name
	}
	for name, a := range d.st.Used {
		if a == addr {
			return name
		}
	}
	return ""
}

// mutedHereLocked says session id of this device has muted whoever sent e, so e isn't queued for
// it.
//
// A message sent to the session itself still reaches it for MuteGrace after the mute (#466,
// Tyler, 2026-11-11). The sender's daemon refuses such a message once it has read of the mute in
// presence, which takes seconds; one sent before that would otherwise be dropped here with its
// sender sure it had arrived. The grace is longer than a presence takes to be pushed and read,
// and goes by this device's clock alone: when the mute was made, and now. After it, the mute
// holds against every sender, whatever its daemon knows or its version. A message to everyone or
// to the device is dropped from the first moment: its sender is never refused, so there is
// nothing for a grace to keep true. Caller holds mu.
func (d *Daemon) mutedHereLocked(id string, e bus.Event) bool {
	m := d.st.Mutes[d.tagKeyLocked(id)]
	if len(m) == 0 {
		return false
	}
	at, muted := m[d.nameOfLocked(e.From)]
	if !muted {
		return false
	}
	direct := strings.Contains(e.To, "/")
	return !direct || time.Since(at) >= d.opt.MuteGrace
}

// hasMutedLocked says the session at dev/sess, here or in another device's presence, has muted
// the session called name. Caller holds mu.
func (d *Daemon) hasMutedLocked(dev, sess, name string) bool {
	if name == "" || sess == "" {
		return false
	}
	if dev == d.device {
		_, muted := d.st.Mutes[d.tagKeyLocked(sess)][name]
		return muted
	}
	if p := d.peers[dev]; p != nil {
		for _, s := range p.pres.Sessions {
			if s.ID == sess {
				return slices.Contains(s.Muted, name)
			}
		}
	}
	return false
}

// mutersLocked lists, by name, the sessions that have muted the session called name: on device
// dev, or on every device this daemon knows if dev is "". Caller holds mu.
func (d *Daemon) mutersLocked(name, dev string) []string {
	if name == "" {
		return nil
	}
	var out []string
	if dev == "" || dev == d.device {
		for _, s := range d.sortedSessionsLocked() {
			if _, muted := d.st.Mutes[tagKeyOf(s.name, s.id)][name]; muted {
				out = append(out, nameOr(s.name, bus.Addr(d.device, s.id)))
			}
		}
	}
	for _, id := range sortedKeys(d.peers) {
		if dev != "" && dev != id {
			continue
		}
		for _, s := range d.peers[id].pres.Sessions {
			if slices.Contains(s.Muted, name) {
				out = append(out, nameOr(s.Name, bus.Addr(id, s.ID)))
			}
		}
	}
	return out
}

// mutersNote tells the sender of a message to many which of them didn't get it, "" if all did.
func mutersNote(muters []string) string {
	switch len(muters) {
	case 0:
		return ""
	case 1:
		return muters[0] + " did not get this message: they have muted you."
	}
	return strings.Join(muters[:len(muters)-1], ", ") + " and " + muters[len(muters)-1] +
		" did not get this message: they have muted you."
}

// mute is OpMute: session id mutes the session called req.Name, or with Unmute undoes that. With
// no name it only lists whom it has muted.
func (d *Daemon) mute(id string, req proto.Request) proto.Response {
	from := bus.Addr(d.device, id)
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.sessions[id]
	if s == nil {
		return fail("the CLI mutes nobody: a mute is a session's")
	}
	key := tagKeyOf(s.name, s.id)
	name := req.Name
	var note string
	switch {
	case name == "":
	case !names.Valid(name):
		return fail(fmt.Sprintf("%q isn't a session's name. A mute takes a name from who, like quiet-otter; a person can't be muted.", name))
	case req.Unmute:
		if _, muted := d.st.Mutes[key][name]; !muted {
			return fail("you haven't muted " + name)
		}
		d.dropMuteLocked(key, name)
		d.log.Printf("session %s unmuted %s", from, name)
		note = "You have unmuted " + name + ". What it sends from now on will reach you."
	case name == s.name:
		return fail("that is your own name")
	default:
		if _, ok := d.resolveNameLocked(name); !ok {
			return fail(d.noSuchNameLocked(name))
		}
		if _, muted := d.st.Mutes[key][name]; !muted && len(d.st.Mutes[key]) >= bus.MaxMuted {
			return fail(fmt.Sprintf("you have muted %d sessions, which is the most a session can; unmute one first", bus.MaxMuted))
		}
		if d.st.Mutes == nil {
			d.st.Mutes = map[string]map[string]time.Time{}
		}
		if d.st.Mutes[key] == nil {
			d.st.Mutes[key] = map[string]time.Time{}
		}
		if _, muted := d.st.Mutes[key][name]; !muted {
			d.st.Mutes[key][name] = time.Now().UTC()
			d.saveLocked()
			d.kickPresence()
			d.log.Printf("session %s muted %s", from, name)
		}
		// The words are Tyler's (2026-11-11): the first version also said who is told and who can
		// undo it, which was more than the caller needs.
		note = "You have muted " + name + ". Nothing it sends will reach you, until you unmute it."
	}
	return proto.Response{OK: true, Self: from, Note: note, Muted: sortedKeys(d.st.Mutes[key])}
}

// dropMuteLocked takes the mute of name off the session whose mutes are kept by key. Caller
// holds mu.
func (d *Daemon) dropMuteLocked(key, name string) {
	delete(d.st.Mutes[key], name)
	if len(d.st.Mutes[key]) == 0 {
		delete(d.st.Mutes, key)
	}
	d.saveLocked()
	d.kickPresence()
}

// muteViewsLocked is every mute this daemon knows, for the page: this device's sessions', which
// its human can undo, then those in other devices' presences. Caller holds mu.
func (d *Daemon) muteViewsLocked() []web.Mute {
	out := []web.Mute{}
	for _, by := range sortedKeys(d.st.Mutes) {
		if !names.Valid(by) {
			continue // a session whose name is pending: the page goes by names
		}
		for _, muted := range sortedKeys(d.st.Mutes[by]) {
			out = append(out, web.Mute{By: by, Muted: muted, At: d.st.Mutes[by][muted], CanUnmute: true})
		}
	}
	for _, dev := range sortedKeys(d.peers) {
		for _, s := range d.peers[dev].pres.Sessions {
			for _, muted := range s.Muted {
				if s.Name != "" {
					out = append(out, web.Mute{By: s.Name, Muted: muted})
				}
			}
		}
	}
	return out
}

// Unmute is the page's: this device's human undoes the mute by the session called by, one of
// this device's, of the session called muted. Another device's mutes are its own human's to undo.
func (s pageSource) Unmute(ctx context.Context, by, muted string) error {
	d := s.d
	d.pageCalled(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.st.Mutes[by][muted]; !ok || !names.Valid(by) {
		return errors.New(by + " hasn't muted " + muted + " on this device")
	}
	d.dropMuteLocked(by, muted)
	d.log.Printf("the page unmuted %s for session %s", muted, by)
	return nil
}
