package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/names"
	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/web"
)

// Mutes (#27, docs/design/mutes.md). A session mutes another by its name, and from then on
// gets nothing the other sends: not a message to it, to its device, or to everyone. Its own device
// decides, by not queueing the message, and publishes the mutes in presence, so they are on the
// bus for anyone on the team to see, a human on the page among them. The muted session isn't
// told when it happens. It learns of it when it next sends to the session that muted it: its own
// daemon, which has read that presence, refuses the send and says why. That is the tool's answer,
// from Murmur and no agent, so it starts no turn anywhere. One whose daemon hadn't read of the mute
// yet is told afterwards that its message was dropped (#466, below).
//
// Mutes are kept as tags are, by the muting session's name, which stays the same while its
// conversation does, and name the muted session by its name, which the registry never gives out
// twice: a mute can't land on another session.

// mutesOfLocked is the names session id of this device has muted, in order. Caller holds mu.
func (d *Daemon) mutesOfLocked(id string) []string {
	return sortedKeys(d.st.Mutes[d.tagKeyLocked(id)])
}

// adoptMutesLocked moves the mutes a session made while its name was pending to the name, once
// it lands. Where the name has mutes already, from before a resume, they are added to those:
// tags kept by the ID give way to the name's (adoptTagsLocked), but a mute dropped lets through
// what the session asked not to get (#483). The name's own stay as they are, and no more are
// added than a session may have. Caller holds mu.
func (d *Daemon) adoptMutesLocked(s *session) {
	pending := tagKeyOf("", s.id)
	m := d.st.Mutes[pending]
	if s.name == "" || m == nil {
		return
	}
	delete(d.st.Mutes, pending)
	have := d.st.Mutes[s.name]
	if have == nil {
		d.st.Mutes[s.name] = m
	} else {
		for _, name := range sortedKeys(m) {
			if _, ok := have[name]; ok {
				continue
			}
			if len(have) >= bus.MaxMuted {
				d.log.Printf("session %s muted %s before its name %s landed, and the name has %d mutes already; not kept",
					bus.Addr(d.device, s.id), name, s.name, len(have))
				continue
			}
			have[name] = m[name]
		}
	}
	d.saveLocked()
}

// nameOfLocked is the name of the session at addr: the one it has in presence, else the one shown
// for it when it has none there (lastNameLocked). "" for an address with none, as the CLI's or a
// person's. Caller holds mu.
func (d *Daemon) nameOfLocked(addr string) string {
	if bus.IsPerson(addr) {
		return ""
	}
	if name, _, _ := d.sessionInfoLocked(addr); name != "" {
		return name
	}
	return d.lastNameLocked(addr)
}

// lastNameLocked is the name a session is shown by when it has none in presence: it has left, or
// its new name hasn't landed yet (a follow to a new conversation, as after /clear, leaves it with
// none until the name is pushed). It is the same every time and in every place: the inbox,
// activity, lookup and the mute checks (#502), so the name an agent replies to or mutes is the one
// it was shown. One of this device's goes by the name it took last (SessionNames), which this
// device knows; for one waiting on a new name, that is its previous one. Another device's goes by
// the first by spelling of the names the registry gave its address, which says nothing of which is
// the latest: neither the registry nor presence keeps an order of them. namesByAddrLocked gives
// the same for every address at once. "" for an address with none. Caller holds mu.
func (d *Daemon) lastNameLocked(addr string) string {
	if dev, id := bus.SplitAddr(addr); dev == d.device && d.st.SessionNames[id] != "" {
		return d.st.SessionNames[id]
	}
	if names := d.heldNamesLocked(addr); len(names) > 0 {
		return names[0]
	}
	return ""
}

// heldNamesLocked is every name the session at addr is known to have held, sorted by spelling:
// those the registry gave the address, and for one of this device's, the name it took last, which
// the registry may have down for another session ID (a conversation resumed under a new one). An
// address holds more than one when its client started a new conversation in the same session
// (Claude Code's /clear). Caller holds mu.
func (d *Daemon) heldNamesLocked(addr string) []string {
	var names []string
	for name, a := range d.st.Used {
		if a == addr {
			names = append(names, name)
		}
	}
	if dev, id := bus.SplitAddr(addr); dev == d.device {
		if name := d.st.SessionNames[id]; name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// mutedAtLocked says session id of this device has muted whoever is at addr, and when. A sender
// in presence goes by the name it has now. One that isn't goes by every name its address has
// held: which of them it had when it sent can't be known, and a map's order must not decide
// (#483), so a mute of any of them holds. The cost is a conversation nobody muted, at an address
// whose earlier conversation was: what it sends is dropped if it has left presence by the time
// this device reads it, and its sender isn't told (docs/design/mutes.md). Caller holds mu.
func (d *Daemon) mutedAtLocked(id, addr string) (at time.Time, muted bool) {
	m := d.st.Mutes[d.tagKeyLocked(id)]
	if len(m) == 0 || bus.IsPerson(addr) {
		return at, false
	}
	if name, _, _ := d.sessionInfoLocked(addr); name != "" {
		at, muted = m[name]
		return at, muted
	}
	for _, name := range d.heldNamesLocked(addr) {
		// The latest mute of them, so the grace is that one's.
		if t, ok := m[name]; ok && (!muted || t.After(at)) {
			at, muted = t, true
		}
	}
	return at, muted
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
	at, muted := d.mutedAtLocked(id, e.From)
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

// muter is a session that has muted another: its address, and its name, or its address where it
// has none.
type muter struct{ addr, name string }

// mutersLocked lists the sessions that have muted the session called name: on device dev, or on
// every device this daemon knows if dev is "". Caller holds mu.
func (d *Daemon) mutersLocked(name, dev string) []muter {
	if name == "" {
		return nil
	}
	var out []muter
	if dev == "" || dev == d.device {
		for _, s := range d.sortedSessionsLocked() {
			if _, muted := d.st.Mutes[tagKeyOf(s.name, s.id)][name]; muted {
				addr := bus.Addr(d.device, s.id)
				out = append(out, muter{addr, nameOr(s.name, addr)})
			}
		}
	}
	for _, id := range sortedKeys(d.peers) {
		if dev != "" && dev != id {
			continue
		}
		for _, s := range d.peers[id].pres.Sessions {
			if slices.Contains(s.Muted, name) {
				addr := bus.Addr(id, s.ID)
				out = append(out, muter{addr, nameOr(s.Name, addr)})
			}
		}
	}
	return out
}

// mutersNote tells the sender of a message to many which of them didn't get it, "" if all did.
func mutersNote(muters []string) string {
	if len(muters) == 0 {
		return ""
	}
	return andList(muters) + " did not get this message: they have muted you."
}

// andList is names as a list in words: "a", "a and b", "a, b and c".
func andList(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// Telling a sender its message was dropped for a mute (#466). The muted session's daemon refuses a
// send to the session that muted it once it has read the mute, and names that session in the result
// of a send to many. One it hadn't read of yet, as when the muting device couldn't push its
// presence for longer than MuteGrace, or was down, sent the message, and the muting device dropped
// it. That device marks the drop (markMutedLocked), and the mark comes back in a receipt like any
// other. This device keeps its sessions' newest messages (Sends) so the mark finds its sender, which
// is told once, by a notice from Murmur in its inbox, about that message, and woken by nothing.

// maxSends is how many of this device's sessions' messages are kept for that, the newest.
const maxSends = 100

// sendRecord is one of this device's sessions' messages that a mute on another device could drop.
type sendRecord struct {
	At      time.Time `json:"at"`
	From    string    `json:"from"`
	To      string    `json:"to"`
	Subject string    `json:"subject"`
	// Told is the sessions, by address, that its sender knows didn't get it, as they have muted it:
	// those send's result named, and those a notice has told of since. Each is told of once.
	Told []string `json:"told,omitempty"`
}

// recordSendLocked keeps message e, which session e.From of this device has sent, for a mark saying
// a mute dropped it; told is the sessions send's result named as having muted the sender. Only a
// message that can reach another device is kept, from a session with a name, as a mute names one.
// Caller holds mu.
func (d *Daemon) recordSendLocked(e bus.Event, told []string) {
	if bus.IsPerson(e.From) || d.nameOfLocked(e.From) == "" {
		return
	}
	if dev, _ := bus.SplitAddr(e.To); dev == d.device {
		return // its own sessions' mutes are this daemon's, which refused it or named them
	}
	if d.st.Sends == nil {
		d.st.Sends = map[string]*sendRecord{}
	}
	d.st.Sends[e.ID] = &sendRecord{At: e.At, From: e.From, To: e.To, Subject: bus.OneLine(e.Subject, 60), Told: told}
	if over := len(d.st.Sends) - maxSends; over > 0 {
		ids := sortedKeys(d.st.Sends)
		slices.SortStableFunc(ids, func(a, b string) int { return d.st.Sends[a].At.Compare(d.st.Sends[b].At) })
		for _, id := range ids[:over] {
			delete(d.st.Sends, id)
		}
	}
	d.saveLocked()
}

// tellMutedLocked takes in marks, from a receipt on device dev's log, saying messages were dropped
// for sessions there that have muted their senders. The sender of each message of this device's
// is told, in one notice per message, of those it didn't know of. A mark from a device the message
// couldn't have reached is dropped: any device can write marks, but only for its own sessions. The
// caller saves the state. Caller holds mu.
func (d *Daemon) tellMutedLocked(dev string, marks []bus.Mark) {
	byMsg := map[string][]string{}
	var order []string
	for _, m := range marks {
		rec := d.st.Sends[m.Msg]
		if rec == nil {
			continue
		}
		addr := bus.Addr(dev, m.To)
		if to, sess := bus.SplitAddr(rec.To); sess != "" {
			if to != dev {
				continue
			}
			addr = rec.To // the session that holds the recipient's name now (forwardLocked) dropped it
		} else if rec.To != "*" && !bus.IsGroup(rec.To) && rec.To != dev {
			continue
		}
		d.untellLocked(m.Msg, addr)
		if slices.Contains(rec.Told, addr) || slices.Contains(byMsg[m.Msg], addr) {
			continue
		}
		if byMsg[m.Msg] == nil {
			order = append(order, m.Msg)
		}
		byMsg[m.Msg] = append(byMsg[m.Msg], addr)
	}
	label := d.labelerLocked()
	for _, msg := range order {
		rec, addrs := d.st.Sends[msg], byMsg[msg]
		rec.Told = append(rec.Told, addrs...)
		dev, sess := bus.SplitAddr(rec.From)
		if _, stopped := d.st.Stopped[sess]; dev != d.device || d.sessions[sess] == nil && !stopped {
			continue // gone, with its inbox
		}
		names := make([]string, 0, len(addrs))
		for _, addr := range sortedBy(addrs, label) {
			names = append(names, label(addr))
		}
		// The words are those of the refusal a send to the session gets once the mute is known.
		subject := andList(names) + " did not get your message " + strconv.Quote(rec.Subject) + ": they have muted you"
		d.addInboxLocked(sess, bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Message,
			From: bus.Addr(d.device, bus.ReceiptFrom), To: rec.From, At: time.Now().UTC(), Subject: subject,
			Body: "Nothing you send can reach them until they unmute you."})
	}
}

// untellLocked takes the session at addr, which has muted the sender, off what message msg's
// sender asked to be told of (#276), if it did: it will never get further. With nobody left to wait
// for, the sender isn't told how far it got, as the notice of the mute said all there was. Caller
// holds mu.
func (d *Daemon) untellLocked(msg, addr string) {
	rec := d.st.Sent[msg]
	if rec == nil || rec.Tell == "" || rec.Told {
		return
	}
	drop := func(a string) bool { return a == addr }
	rec.To, rec.Old = slices.DeleteFunc(rec.To, drop), slices.DeleteFunc(rec.Old, drop)
	delete(rec.Marks, addr)
	if len(waitedFor(rec)) == 0 {
		delete(d.st.Sent, msg)
		return
	}
	d.tellLocked(msg, rec, false)
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
