package daemon

import (
	"slices"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/proto"
)

// A person's message explains, in full, that the person sees only what the agent sends, and how to
// answer, the first time a session is given one from that person (#249). After that it ends with
// the line an agent's message does (#345, Tyler, 2026-11-09): the agent has been told, and a long
// line on every message is noise. Every route that gives a session a message whole asks
// explainedLocked: a turn by T3 or the messaging socket, the hook's whole message, the Stop hook's,
// and inbox. A notice gives the subject alone, with no line, and doesn't count.
//
// What a session has been told is kept in state.json by the session's name, as its tags are (see
// tags.go), so it outlasts the session's MCP server restarting, a resume, and the daemon
// restarting. A session whose name is pending keeps it by its ID until the name lands. A message
// counts once it has reached the session: a turn whose handoff failed, and comes back by the hook,
// explains again.

// explainKeyLocked is what the people session id has been told of are kept by: its tags' key,
// else its ID; "" for the command line, which keeps nothing. Caller holds mu.
func (d *Daemon) explainKeyLocked(id string) string {
	if id == "" || id == proto.CLISession {
		return ""
	}
	if key := d.tagKeyLocked(id); key != "" {
		return key
	}
	return tagKeyOf("", id)
}

// explainedLocked says session id has had a message from person from, so another needs no more
// than an agent's line. Always true for a sender who isn't a person, whose line is that already.
// Caller holds mu.
func (d *Daemon) explainedLocked(id, from string) bool {
	if !bus.IsPerson(from) {
		return true
	}
	key := d.explainKeyLocked(id)
	return key != "" && slices.Contains(d.st.Explained[key], from)
}

// noteExplainedLocked records that session id has had a message from from, if from is a person.
// The caller saves the state. Caller holds mu.
func (d *Daemon) noteExplainedLocked(id, from string) {
	key := d.explainKeyLocked(id)
	if key == "" || d.explainedLocked(id, from) {
		return
	}
	if d.st.Explained == nil {
		d.st.Explained = map[string][]string{}
	}
	d.st.Explained[key] = append(d.st.Explained[key], from)
}

// explainViewsLocked sets each of views' Explained, in order, and records the sessions as told: a
// message is rendered as from a person already explained only if every session it is given to has
// been (to says which those are), since the hook speaks for several sessions at once and shows a
// message once, as wholeViewsLocked does for Introduced. A second message from the same person in
// views is explained already by the first. The caller saves the state. Caller holds mu.
func (d *Daemon) explainViewsLocked(views []proto.MessageView, to func(i int) []string) {
	for i := range views {
		if !bus.IsPerson(views[i].From) {
			continue // an agent's line is the short one already
		}
		ids := to(i)
		views[i].Explained = true
		for _, id := range ids {
			if !d.explainedLocked(id, views[i].From) {
				views[i].Explained = false
			}
		}
		for _, id := range ids {
			d.noteExplainedLocked(id, views[i].From)
		}
	}
}

// adoptExplainedLocked moves what session s kept by its ID while its name was pending to the
// name, once the name lands, as adoptTagsLocked does with its tags. Caller holds mu.
func (d *Daemon) adoptExplainedLocked(s *session) {
	if s.name == "" {
		return
	}
	pending := tagKeyOf("", s.id)
	people, ok := d.st.Explained[pending]
	if !ok {
		return
	}
	delete(d.st.Explained, pending)
	for _, p := range people {
		if !slices.Contains(d.st.Explained[s.name], p) {
			d.st.Explained[s.name] = append(d.st.Explained[s.name], p)
		}
	}
	d.saveLocked()
}
