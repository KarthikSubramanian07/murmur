// Package bus is the wire format and the git plumbing of the bus: events and presence encoded
// as commit messages on refs/t4/*, written and read without ever touching a working tree.
package bus

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Version is the format version every event and presence record carries. A reader skips
// records with a version it doesn't know rather than guessing at them.
const Version = 1

// Event types.
const (
	SessionStarted = "session.started"
	SessionStopped = "session.stopped"
	Message        = "message"
	Claim          = "claim"
	Release        = "release"
)

// Event is one entry in a device's log, or on the claims ref. Fields beyond the first five
// depend on Type.
type Event struct {
	V    int       `json:"v"`
	ID   string    `json:"id"`
	Type string    `json:"type"`
	From string    `json:"from"` // device/session
	At   time.Time `json:"at"`   // the writer's clock; for display, never for ordering

	Agent  string `json:"agent,omitempty"`  // session.started
	Branch string `json:"branch,omitempty"` // session.started

	To      string `json:"to,omitempty"` // message: device/session, device, or "*"
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body,omitempty"`

	What  string   `json:"what,omitempty"`  // claim
	Paths []string `json:"paths,omitempty"` // claim
	// Ends, on a claim, lists the claims its daemon judged lapsed when it was made. Every
	// daemon treats them as ended from then on, so a lapse can't be undone behind the claimer.
	Ends  []string `json:"ends,omitempty"`
	Claim string   `json:"claim,omitempty"` // release: the claim's ID
}

// Line is the event as one readable line, used as the commit subject so that `git log` on a
// bus ref reads like a log.
func (e Event) Line() string {
	switch e.Type {
	case SessionStarted:
		return fmt.Sprintf("%s started (%s, %s)", e.From, e.Agent, e.Branch)
	case SessionStopped:
		return e.From + " stopped"
	case Message:
		return fmt.Sprintf("%s -> %s: %s", e.From, e.To, e.Subject)
	case Claim:
		line := fmt.Sprintf("%s claims %s: %s", e.From, e.ID, e.What)
		if len(e.Ends) > 0 {
			line += " (ending lapsed " + strings.Join(e.Ends, ", ") + ")"
		}
		return line
	case Release:
		return fmt.Sprintf("%s releases %s", e.From, e.Claim)
	}
	return e.From + " " + e.Type
}

// Valid reports whether a decoded event is one this version understands.
//
// Anyone with push access can write the bus, and what it carries reaches agents' context, so a
// reader holds each event to what t4 itself writes: IDs and addresses in their own alphabets
// (Valid), text fields on one line at t4's lengths (Clean, which goes first).
func (e Event) Valid() bool {
	if e.V != Version || !validID(e.ID) || !validAddr(e.From) {
		return false
	}
	switch e.Type {
	case SessionStarted, SessionStopped:
		return true
	case Message:
		return validTo(e.To) && e.Subject != ""
	case Claim:
		for _, id := range e.Ends {
			if !validID(id) {
				return false
			}
		}
		return e.What != ""
	case Release:
		return validID(e.Claim)
	}
	return false
}

// Clean puts each text field of an event read from the bus on one line, at most as long as t4
// itself writes it. Bodies keep their lines; the reader's view marks them as quoted.
func (e *Event) Clean() {
	e.Subject = OneLine(e.Subject, 120)
	e.What = OneLine(e.What, 200)
	e.Agent = OneLine(e.Agent, 40)
	e.Branch = OneLine(e.Branch, 100)
	for i, p := range e.Paths {
		e.Paths[i] = OneLine(p, 200)
	}
}

// Clean does for a presence what Event.Clean does for an event, and drops sessions whose IDs
// t4 wouldn't write.
func (p *Presence) Clean() {
	p.Host = OneLine(p.Host, 64)
	p.Owner = OneLine(p.Owner, 64)
	var keep []SessionInfo
	for _, s := range p.Sessions {
		if ValidName(s.ID) {
			s.Agent = OneLine(s.Agent, 40)
			s.Branch = OneLine(s.Branch, 100)
			s.Doing = OneLine(s.Doing, 120)
			keep = append(keep, s)
		}
	}
	p.Sessions = keep
}

// ValidName accepts device and session IDs: lowercase letters, digits and dashes.
func ValidName(s string) bool {
	if s == "" || len(s) > 40 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// validID accepts event IDs: lowercase hex digits, as NewID makes them.
func validID(s string) bool {
	if len(s) < 8 || len(s) > 40 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// validAddr accepts a sender: device/session.
func validAddr(a string) bool {
	dev, sess := SplitAddr(a)
	return ValidName(dev) && ValidName(sess)
}

// validTo accepts a recipient: everyone, a device, or a session.
func validTo(to string) bool {
	if to == "*" {
		return true
	}
	if !strings.Contains(to, "/") {
		return ValidName(to)
	}
	return validAddr(to)
}

// Presence is a device's current state, the whole of its presence ref.
type Presence struct {
	V        int           `json:"v"`
	Device   string        `json:"device"`
	Host     string        `json:"host"`
	Owner    string        `json:"owner,omitempty"` // git user.name on that clone
	At       time.Time     `json:"at"`              // the writer's clock
	Sessions []SessionInfo `json:"sessions"`
}

// SessionInfo is one session as its device's presence describes it.
type SessionInfo struct {
	ID      string    `json:"id"`
	Agent   string    `json:"agent"`
	Branch  string    `json:"branch,omitempty"`
	Doing   string    `json:"doing,omitempty"`
	Started time.Time `json:"started"`
}

// Encode renders a record as a commit message: a readable subject line, a blank line, then
// the JSON that readers actually parse.
func Encode(v any, subject string) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return OneLine(subject, 120) + "\n\n" + string(b) + "\n", nil
}

// Decode parses a commit message written by Encode.
func Decode(msg string, v any) error {
	i := strings.Index(msg, "\n\n")
	if i < 0 {
		return errors.New("no JSON body")
	}
	return json.Unmarshal([]byte(strings.TrimSpace(msg[i+2:])), v)
}

// OneLine collapses whitespace, newlines included, and cuts s to at most max runes.
func OneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max-1]) + "…"
	}
	return s
}

// Addr joins a device and a session into an address.
func Addr(device, session string) string { return device + "/" + session }

// SplitAddr splits an address; a bare device has an empty session.
func SplitAddr(addr string) (device, session string) {
	if i := strings.IndexByte(addr, '/'); i >= 0 {
		return addr[:i], addr[i+1:]
	}
	return addr, ""
}

// RandHex returns n random hex digits.
func RandHex(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)[:n]
}

// NewID returns a fresh event ID.
func NewID() string { return RandHex(8) }
