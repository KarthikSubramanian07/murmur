// Package tags is session tags (#198): short labels on a session that every teammate's agents see
// in who, lookup and the page, and can search by. A tag comes from one of three places, highest
// first: a human on the page, the session itself, or the daemon, which derives a few from facts it
// already has (the model, where T3 records it). The session's own device decides the tags of its
// sessions and publishes them in presence; docs/VISION.md, "Session tags", has the reasons.
//
// One tag carries behaviour: do-not-disturb, set by the session or a human, keeps the session's
// messages queued in its inbox. None is handed over as a turn or delivered by the hook until the
// session reads its inbox or the tag comes off. A tag under model: names the model the session runs, and a
// session has at most one.
package tags

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	// DoNotDisturb keeps a session's messages queued in its inbox: while it has the tag none is
	// handed over as a turn, and the hook delivers neither a notice nor the whole message. They are
	// delivered when the session reads its inbox, or by whichever route applies once the tag comes
	// off.
	DoNotDisturb = "do-not-disturb"
	// ModelPrefix starts the tag naming the session's model. A session has at most one.
	ModelPrefix = "model:"
	// MaxLen caps a tag's length, and MaxPerSession how many a session has.
	MaxLen        = 40
	MaxPerSession = 12
)

// Where a tag came from.
const (
	SrcSession = ""        // the session set it for itself
	SrcDerived = "derived" // the daemon worked it out
	SrcHuman   = "human"   // a human set it on the page
)

// Tag is one tag on a session, with where it came from.
type Tag struct {
	Name string `json:"name"`
	Src  string `json:"src,omitempty"`
}

var shape = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*$`)

// Normalize is how a tag a person or an agent typed is read: trimmed and in lower case.
func Normalize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// Valid says whether name is a tag: lowercase letters, digits and . _ : / -, starting with a letter or
// a digit, at most MaxLen long.
func Valid(name string) bool {
	return len(name) <= MaxLen && shape.MatchString(name) && name != ModelPrefix
}

// family is the group of tags a session has at most one of: every model: tag is one family; any
// other tag is a family of its own.
func family(name string) string {
	if strings.HasPrefix(name, ModelPrefix) {
		return ModelPrefix
	}
	return name
}

// ModelTag is the tag for a model ID, as a client records it: claude-opus-5-5 is model:opus-5.5,
// gpt-5.1-codex is model:gpt-5.1-codex. A Claude model loses its claude- prefix and any date
// suffix, and a version written with a dash between two numbers at the end takes a dot. "" for an
// ID that makes no tag.
func ModelTag(id string) string {
	m := Normalize(id)
	m = strings.TrimPrefix(m, "claude-")
	m = dateSuffix.ReplaceAllString(m, "")
	m = dashVersion.ReplaceAllString(m, "-$1.$2")
	m = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r) {
			return r
		}
		return '-'
	}, m)
	if m == "" {
		return ""
	}
	t := ModelPrefix + m
	if !Valid(t) {
		return ""
	}
	return t
}

var (
	dateSuffix  = regexp.MustCompile(`-\d{8}$`)
	dashVersion = regexp.MustCompile(`-(\d)-(\d{1,2})$`)
)

// Has says whether ts holds a tag called name.
func Has(ts []Tag, name string) bool {
	return slices.ContainsFunc(ts, func(t Tag) bool { return t.Name == name })
}

// Names lists the tags' names, in order.
func Names(ts []Tag) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

// Mark is a human's choice about one tag on one session: on, the session has it whatever the
// session or the daemon says; off, it doesn't.
type Mark struct {
	On bool      `json:"on"`
	By string    `json:"by,omitempty"` // the person, as git's user.name on their clone, or their device
	At time.Time `json:"at"`
}

// State is what a session's device keeps about its tags: those the session set, and a human's
// marks. Derived tags are worked out afresh each time and never kept. Cleared is when a human last
// took a tag away leaving no mark: it decides nothing about the session's tags, and is kept only so
// that a change from before then, read late, doesn't bring the tag back.
type State struct {
	Self    []string             `json:"self,omitempty"`
	Human   map[string]Mark      `json:"human,omitempty"`
	Cleared map[string]time.Time `json:"cleared,omitempty"`
}

// Empty says there is nothing to keep.
func (st *State) Empty() bool {
	return st == nil || len(st.Self) == 0 && len(st.Human) == 0 && len(st.Cleared) == 0
}

// clockAllowance is how much older than a human's last choice a change can be and still go by the
// order the two were read in, as they come from different machines' clocks.
const clockAllowance = time.Minute

// LastChoice is when a human last chose anything about these tags, zero if never.
func (st *State) LastChoice() time.Time {
	var last time.Time
	if st == nil {
		return last
	}
	for _, m := range st.Human {
		if m.At.After(last) {
			last = m.At
		}
	}
	for _, at := range st.Cleared {
		if at.After(last) {
			last = at
		}
	}
	return last
}

// chosenSince says a human made a choice about tag t that a change made at at is too old to undo.
func (st *State) chosenSince(t string, at time.Time) bool {
	limit := at.Add(clockAllowance)
	return st.Human[t].At.After(limit) || st.Cleared[t].After(limit)
}

// familyChosenSince says a human gave the session another tag of t's family since at.
func (st *State) familyChosenSince(t string, at time.Time) bool {
	limit := at.Add(clockAllowance)
	for o, m := range st.Human {
		if o != t && family(o) == family(t) && m.On && m.At.After(limit) {
			return true
		}
	}
	return false
}

// Clean reads a list of tags as typed, each normalized. It fails on the first that isn't a tag.
func Clean(in []string) (names []string, err error) {
	for _, raw := range in {
		t := Normalize(raw)
		if t == "" {
			continue
		}
		if !Valid(t) {
			return nil, fmt.Errorf("%q isn't a tag: a tag is up to %d lowercase letters, digits and . _ : / -, starting with a letter or a digit", raw, MaxLen)
		}
		if !slices.Contains(names, t) {
			names = append(names, t)
		}
	}
	return names, nil
}

// SetSelf applies the session's own change: add, then remove. A tag added replaces any other of
// its family (a second model: tag replaces the first). It fails, and changes nothing, if the
// session would have more than MaxPerSession.
func (st *State) SetSelf(add, remove []string) error {
	add, err := Clean(add)
	if err != nil {
		return err
	}
	remove, err = Clean(remove)
	if err != nil {
		return err
	}
	next := slices.Clone(st.Self)
	for _, t := range add {
		next = slices.DeleteFunc(next, func(o string) bool { return family(o) == family(t) })
		next = append(next, t)
	}
	next = slices.DeleteFunc(next, func(o string) bool { return slices.Contains(remove, o) })
	if len(next) > MaxPerSession {
		return fmt.Errorf("a session has at most %d tags", MaxPerSession)
	}
	slices.Sort(next)
	st.Self = next
	return nil
}

// SetHuman applies a human's change, given the tags the session has without the human's marks
// (base: its own and the derived ones). Adding marks a tag on, and marks off the rest of its
// family in base. Removing marks a tag off if base has it, and otherwise takes away the human's
// mark on it, so a tag only a human gave goes with no mark left behind. A removal of a tag the
// session doesn't have (a page that was behind) leaves no mark either: the human turned off nothing,
// and a mark there would stop the session from ever setting the tag, until a human cleared a mark
// the page can't show.
//
// A change can be read long after it was made, and two can be read in either order, so each tag goes
// by the human's latest choice about it (#251): a change older than that, by more than clockAllowance,
// leaves the tag as it is. The session's own changes carry no time, and a human's beat them anyway.
func (st *State) SetHuman(base []string, add, remove []string, by string, at time.Time) error {
	add, err := Clean(add)
	if err != nil {
		return err
	}
	remove, err = Clean(remove)
	if err != nil {
		return err
	}
	if len(add)+len(remove) == 0 {
		return errors.New("no tag to add or remove")
	}
	if st.Human == nil {
		st.Human = map[string]Mark{}
	}
	mark := func(t string, on bool) {
		st.Human[t] = Mark{On: on, By: by, At: at}
		delete(st.Cleared, t)
	}
	for _, t := range add {
		if st.chosenSince(t, at) || st.familyChosenSince(t, at) {
			continue
		}
		for o, m := range st.Human {
			if o != t && family(o) == family(t) && m.On {
				delete(st.Human, o)
			}
		}
		for _, o := range base {
			if o != t && family(o) == family(t) && !st.chosenSince(o, at) {
				mark(o, false)
			}
		}
		mark(t, true)
	}
	for _, t := range remove {
		switch {
		case st.chosenSince(t, at):
		case slices.Contains(base, t):
			mark(t, false)
		default:
			delete(st.Human, t)
			if st.Cleared == nil {
				st.Cleared = map[string]time.Time{}
			}
			st.Cleared[t] = at
		}
	}
	return nil
}

// Base is the tags a session has before a human's marks: its own, and the derived ones where it
// set none of their family.
func Base(self, derived []string) []string {
	out := slices.Clone(self)
	for _, t := range derived {
		if !slices.ContainsFunc(out, func(o string) bool { return family(o) == family(t) }) {
			out = append(out, t)
		}
	}
	return out
}

// Effective is the tags a session has, each with where it came from: its own and the derived ones
// (see Base), then a human's marks over both, in order of name, at most MaxPerSession.
func Effective(self, derived []string, human map[string]Mark) []Tag {
	src := map[string]string{}
	for _, t := range self {
		src[t] = SrcSession
	}
	for _, t := range Base(nil, derived) {
		if _, ok := src[t]; !ok && !slices.ContainsFunc(self, func(o string) bool { return family(o) == family(t) }) {
			src[t] = SrcDerived
		}
	}
	for t, m := range human {
		if m.On {
			// The human's tag is the family's one, whatever the session or the daemon set after.
			for o := range src {
				if o != t && family(o) == family(t) {
					delete(src, o)
				}
			}
			src[t] = SrcHuman
		}
	}
	for t, m := range human {
		if !m.On {
			delete(src, t)
		}
	}
	out := make([]Tag, 0, len(src))
	for t, s := range src {
		out = append(out, Tag{Name: t, Src: s})
	}
	if len(out) > MaxPerSession {
		// A human's tags are kept first, then the session's, then the derived ones.
		rank := map[string]int{SrcHuman: 0, SrcSession: 1, SrcDerived: 2}
		slices.SortFunc(out, func(a, b Tag) int {
			if r := rank[a.Src] - rank[b.Src]; r != 0 {
				return r
			}
			return strings.Compare(a.Name, b.Name)
		})
		out = out[:MaxPerSession]
	}
	Sort(out)
	return out
}

// Sort puts tags in order of name.
func Sort(ts []Tag) {
	slices.SortFunc(ts, func(a, b Tag) int { return strings.Compare(a.Name, b.Name) })
}

// CleanRead holds tags read from the bus to what Murmur writes: valid names from a known source,
// none repeated, at most MaxPerSession. Anyone who can push can write the bus.
func CleanRead(in []Tag) []Tag {
	var out []Tag
	for _, t := range in {
		if !Valid(t.Name) || Has(out, t.Name) || len(out) >= MaxPerSession {
			continue
		}
		switch t.Src {
		case SrcSession, SrcDerived, SrcHuman:
		default:
			t.Src = SrcSession
		}
		out = append(out, t)
	}
	return out
}

// Filter is a search by tags: a session matches when it has every tag asked for and none of those
// asked against. A term is a tag (reviewing), a tag after a dash (-reviewing, without it), or either
// ending in * for any tag that starts so (model:opus*).
type Filter struct {
	terms []term
}

type term struct {
	name   string
	prefix bool
	not    bool
}

// ParseFilter reads search terms, as who takes them. Terms may also come comma-separated in one
// string. No terms is a filter that matches everything.
func ParseFilter(in []string) (Filter, error) {
	var f Filter
	for _, raw := range in {
		for _, part := range strings.Split(raw, ",") {
			t := Normalize(part)
			if t == "" {
				continue
			}
			var tm term
			if rest, ok := strings.CutPrefix(t, "-"); ok {
				tm.not, t = true, rest
			}
			if rest, ok := strings.CutSuffix(t, "*"); ok {
				tm.prefix, t = true, rest
			}
			if t == "" || len(t) > MaxLen || !shape.MatchString(t) {
				return Filter{}, fmt.Errorf("%q isn't a tag to search by: use a tag, -tag for sessions without it, or end it in * for tags that start so", strings.TrimSpace(part))
			}
			tm.name = t
			f.terms = append(f.terms, tm)
		}
	}
	return f, nil
}

// Empty says the filter matches everything.
func (f Filter) Empty() bool { return len(f.terms) == 0 }

// Match says whether a session with tags ts matches.
func (f Filter) Match(ts []Tag) bool {
	for _, tm := range f.terms {
		has := slices.ContainsFunc(ts, func(t Tag) bool {
			if tm.prefix {
				return strings.HasPrefix(t.Name, tm.name)
			}
			return t.Name == tm.name
		})
		if has == tm.not {
			return false
		}
	}
	return true
}

// String is the filter as who echoes it.
func (f Filter) String() string {
	var parts []string
	for _, tm := range f.terms {
		s := tm.name
		if tm.prefix {
			s += "*"
		}
		if tm.not {
			s = "-" + s
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}
