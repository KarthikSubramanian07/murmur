package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/tags"
)

// Session tags (#198, docs/VISION.md, "Session tags"). A session's own device decides its tags and
// publishes them in presence: the tags the session set for itself, those the daemon derives (the
// model, from T3's thread), and a human's marks over both. A human on another device's page sends
// the change to this device as a tags event on their own log, and this device applies it. No
// shared ref is needed: one device is the only writer of its sessions' tags, and a human beats the
// session whatever order the two arrive in.
//
// The tags are kept by the session's key, the one its name follows (see nameKey), so they outlast
// its MCP server restarting, a resume, and the daemon restarting.

// tagStateLocked is what is kept about the tags of session s, nil if nothing. Caller holds mu.
func (d *Daemon) tagStateLocked(s *session) *tags.State {
	return d.st.Tags[s.key]
}

// derivedTagsLocked is the tags the daemon works out for session id: its model, where T3 records
// the thread's. Caller holds mu.
func (d *Daemon) derivedTagsLocked(id string) []string {
	var out []string
	if st, ok := d.threadStateLocked(id); ok {
		if t := tags.ModelTag(st.Model); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// tagsOfLocked is the tags session s has. Caller holds mu.
func (d *Daemon) tagsOfLocked(s *session) []tags.Tag {
	var self []string
	var human map[string]tags.Mark
	if st := d.tagStateLocked(s); st != nil {
		self, human = st.Self, st.Human
	}
	return tags.Effective(self, d.derivedTagsLocked(s.id), human)
}

// peerTagsLocked is the tags of session sess in device p's presence, nil if it isn't there.
func peerTagsLocked(p *peer, sess string) []tags.Tag {
	for _, s := range p.pres.Sessions {
		if s.ID == sess {
			return s.Tags
		}
	}
	return nil
}

// unavailableLocked says the session at dev/sess isn't taking work: send refuses a message to it,
// and a message to its device or to everyone skips it. A session on a device whose presence has
// gone stale is not taken for unavailable: nothing current says so. Caller holds mu.
func (d *Daemon) unavailableLocked(dev, sess string) bool {
	if sess == "" || sess == proto.CLISession {
		return false
	}
	if dev == d.device {
		s := d.sessions[sess]
		return s != nil && tags.Has(d.tagsOfLocked(s), tags.Unavailable)
	}
	p := d.peers[dev]
	return p != nil && !d.staleLocked(p) && tags.Has(peerTagsLocked(p, sess), tags.Unavailable)
}

// unavailableWhy is what a sender is told when send refuses a message to an unavailable session,
// called shown.
func unavailableWhy(shown string) string {
	return fmt.Sprintf("%s is unavailable: it isn't taking work right now, though it still reads what is "+
		"going on. Use who to find another session (who with tags: [\"available\"] lists only those taking work).", shown)
}

// setOwnTags is OpTags: session id adds and removes its own tags. A human's mark on a tag stands
// over the session's change, and the reply says so.
func (d *Daemon) setOwnTags(id string, req proto.Request) proto.Response {
	from := bus.Addr(d.device, id)
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.sessions[id]
	if s == nil {
		return proto.Response{OK: true, Self: from, Note: "the CLI has no tags"}
	}
	st := d.st.Tags[s.key]
	if st == nil {
		st = &tags.State{}
	}
	next := *st
	if err := next.SetSelf(req.Add, req.Remove); err != nil {
		return fail(err.Error())
	}
	var held []string
	added, _, _ := tags.Clean(req.Add)
	removed, _, _ := tags.Clean(req.Remove)
	for _, t := range append(added, removed...) {
		if m, ok := next.Human[t]; ok {
			held = append(held, fmt.Sprintf("%s (a human set it %s)", t, map[bool]string{true: "on", false: "off"}[m.On]))
		}
	}
	d.putTagStateLocked(s.key, &next)
	d.kickPresence()
	note := "tags set"
	if len(held) > 0 {
		note = "tags set, but a human's choice on the page stands over yours for " + strings.Join(held, ", ")
	}
	return proto.Response{OK: true, Self: from, Note: note, Tags: d.tagsOfLocked(s)}
}

// putTagStateLocked keeps st as the tag state of key, or drops it when there is nothing in it,
// and saves. Caller holds mu.
func (d *Daemon) putTagStateLocked(key string, st *tags.State) {
	if st.Empty() {
		delete(d.st.Tags, key)
	} else {
		d.st.Tags[key] = st
	}
	d.saveLocked()
}

// humanTagsLocked applies a human's change to the tags of session id on this device. by is the
// person, or their device. Caller holds mu.
func (d *Daemon) humanTagsLocked(id string, add, remove []string, by string, at time.Time) error {
	s := d.sessions[id]
	if s == nil {
		return fmt.Errorf("session %s isn't running here", bus.Addr(d.device, id))
	}
	var self []string
	var human map[string]tags.Mark
	if st := d.st.Tags[s.key]; st != nil {
		self, human = st.Self, st.Human
	}
	next := tags.State{Self: self, Human: cloneMarks(human)}
	if err := next.SetHuman(tags.Base(self, d.derivedTagsLocked(id)), add, remove, by, at); err != nil {
		return err
	}
	d.putTagStateLocked(s.key, &next)
	d.kickPresence()
	d.log.Printf("%s changed the tags of session %s: %s", by, bus.Addr(d.device, id), tagChange(add, remove))
	return nil
}

func cloneMarks(m map[string]tags.Mark) map[string]tags.Mark {
	out := make(map[string]tags.Mark, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func tagChange(add, remove []string) string {
	var ch []string
	for _, t := range add {
		ch = append(ch, "+"+t)
	}
	for _, t := range remove {
		ch = append(ch, "-"+t)
	}
	return strings.Join(ch, " ")
}

// applyTagsEventLocked applies a human's change made on another device's page, if it is for a
// session here. Caller holds mu.
func (d *Daemon) applyTagsEventLocked(e bus.Event) {
	dev, sess := bus.SplitAddr(e.To)
	if dev != d.device || d.sessions[sess] == nil {
		return
	}
	from, _ := bus.SplitAddr(e.From)
	by := from
	if p := d.peers[from]; p != nil && p.pres.Owner != "" {
		by = p.pres.Owner + " on " + from
	}
	if err := d.humanTagsLocked(sess, e.Add, e.Remove, by, e.At); err != nil {
		d.log.Printf("tags from %s for %s not applied: %v", e.From, e.To, err)
	}
}

// SetTags is the page's change to the tags of the session at addr: applied here for a session of
// this device, sent to its device as a tags event otherwise.
func (s pageSource) SetTags(ctx context.Context, addr string, add, remove []string) error {
	d := s.d
	d.pageCalled(ctx)
	add, availAdd, err := tags.Clean(add)
	if err != nil {
		return err
	}
	remove, availRemove, err := tags.Clean(remove)
	if err != nil {
		return err
	}
	if availAdd {
		add = append(add, tags.Available)
	}
	if availRemove {
		remove = append(remove, tags.Available)
	}
	if len(add)+len(remove) == 0 {
		return errors.New("no tag to add or remove")
	}
	if len(add) > tags.MaxPerSession || len(remove) > tags.MaxPerSession {
		return fmt.Errorf("at most %d tags at a time", tags.MaxPerSession)
	}
	dev, sess := bus.SplitAddr(addr)
	if !bus.ValidDevice(dev) || !bus.ValidName(sess) {
		return errors.New("not a session's address")
	}
	d.mu.Lock()
	if dev == d.device {
		by := d.owner
		if by == "" {
			by = d.device
		}
		err := d.humanTagsLocked(sess, add, remove, by, time.Now().UTC())
		d.mu.Unlock()
		return err
	}
	p := d.peers[dev]
	known := p != nil && d.addressKnownLocked(dev, sess)
	d.mu.Unlock()
	if !known {
		return fmt.Errorf("%s isn't in its device's presence", addr)
	}
	e := bus.Event{V: bus.Version, ID: bus.NewID(), Type: bus.Tags, From: bus.Addr(d.device, proto.CLISession),
		At: time.Now().UTC(), To: addr, Add: add, Remove: remove}
	return d.appendEvent(ctx, e)
}
