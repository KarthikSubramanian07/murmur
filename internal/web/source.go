package web

import (
	"context"
	"time"

	"github.com/a-Fig/murmur/internal/tags"
)

// Source is everything the page shows and changes. The daemon implements it; the page reaches
// the daemon through nothing else. Each method's ctx says, through Watched, whether the page that
// called can be seen.
type Source interface {
	// State is this device, every device the daemon knows of with its sessions, and the mutes.
	State(ctx context.Context) (State, error)
	// Messages returns the messages after cursor, oldest first, and the cursor to pass next
	// time. An empty cursor asks for the most recent ones, as many as the source keeps.
	Messages(ctx context.Context, cursor string) (msgs []Message, next string, err error)
	// Settings returns the human's settings for this device.
	Settings(ctx context.Context) (Settings, error)
	// SetSettings replaces them, saved so they outlast a restart, and returns them as saved.
	SetSettings(ctx context.Context, s Settings) (Settings, error)
	// Unmute undoes the mute by session by of session muted, both names.
	Unmute(ctx context.Context, by, muted string) error
	// SetTags is a human's change to the tags of the session at addr, its device/session: add, then
	// remove. On another device, the change takes effect when that device reads it.
	SetTags(ctx context.Context, addr string, add, remove []string) error
}

// State is what the page's sessions view and mute list show.
type State struct {
	Device  string   `json:"device"` // this device's ID
	Devices []Device `json:"devices"`
	Mutes   []Mute   `json:"mutes"`
}

// Device is one device: one clone's daemon.
type Device struct {
	ID    string `json:"id"`
	Host  string `json:"host"`
	Owner string `json:"owner,omitempty"`
	Self  bool   `json:"self,omitempty"`
	// Reachable says the device's heartbeat is current, so its sessions can get messages now.
	Reachable bool      `json:"reachable"`
	LastSeen  time.Time `json:"lastSeen"` // when this daemon last saw the device's presence change
	Sessions  []Session `json:"sessions"`
}

// Session is one agent session.
type Session struct {
	Name string `json:"name"` // two words, like quiet-otter; also its address
	// Addr is its address on the bus, device/session. Name is Addr until the session has a name,
	// which can land after its first messages; the page goes by Addr to tie the two together.
	Addr   string `json:"addr,omitempty"`
	Agent  string `json:"agent"` // claude-code, codex, ...
	Branch string `json:"branch,omitempty"`
	// Committed is the branches the session has committed to, most recent first (#21).
	Committed []string `json:"committed,omitempty"`
	Doing     string   `json:"doing,omitempty"` // the status line the session set, else its conversation's title
	// Worktree is the name of the session's worktree folder. Only sessions on this device have
	// one: it never leaves the machine.
	Worktree   string    `json:"worktree,omitempty"`
	Started    time.Time `json:"started"`
	LastActive time.Time `json:"lastActive,omitzero"` // its last tool call or request; zero if unknown
	// Tags is the session's tags, in order of name, each with where it came from: the session,
	// derived by its daemon, or a human (#198).
	Tags []tags.Tag `json:"tags,omitempty"`
}

// Message is one message between sessions.
type Message struct {
	ID      string    `json:"id"`
	From    string    `json:"from"`   // the sender's name
	To      string    `json:"to"`     // a name, a device ID, or "*"
	ToKind  string    `json:"toKind"` // ToSession, ToDevice or ToAll
	Subject string    `json:"subject"`
	Body    string    `json:"body,omitempty"`
	At      time.Time `json:"at"` // the sender's clock
	// FromAddr and ToAddr are what the bus carried: the sender's device/session, and the
	// recipient's device/session, device ID or "*". A name that lands later then reaches a
	// message the page already holds.
	FromAddr string `json:"fromAddr,omitempty"`
	ToAddr   string `json:"toAddr,omitempty"`
}

// Message.ToKind values.
const (
	ToSession = "session"
	ToDevice  = "device"
	ToAll     = "all"
)

// Mute is one session's mute of another: the muted session's messages to it are not delivered.
type Mute struct {
	By    string    `json:"by"`
	Muted string    `json:"muted"`
	At    time.Time `json:"at"`
	// CanUnmute says this device may undo the mute, so the page offers to.
	CanUnmute bool `json:"canUnmute"`
}

// Settings are the human's choices for this device.
type Settings struct {
	// Wake is the device-wide default for whether a message may wake an idle session.
	Wake Wake `json:"wake"`
	// SessionWake overrides Wake for single sessions on this device, by name.
	SessionWake map[string]Wake `json:"sessionWake,omitempty"`
	// DefaultMinutes is what Wake.Minutes 0 stands for: 3, or the prompt-cache length if known.
	// The source reports it; SetSettings ignores it.
	DefaultMinutes int `json:"defaultMinutes"`
}

// Wake is one choice of whether a message from another session may wake an idle session.
type Wake struct {
	When string `json:"when"` // WakeAlways, WakeWithin or WakeNever
	// Minutes, for WakeWithin, is how long a session may have been idle and still be woken.
	// 0 means Settings.DefaultMinutes.
	Minutes int `json:"minutes,omitempty"`
}

// Wake.When values.
const (
	WakeAlways = "always"
	WakeWithin = "within"
	WakeNever  = "never"
)

// MaxWakeMinutes bounds Wake.Minutes: a day.
const MaxWakeMinutes = 24 * 60

func (w Wake) valid() bool {
	switch w.When {
	case WakeAlways, WakeNever:
		return w.Minutes == 0
	case WakeWithin:
		return w.Minutes >= 0 && w.Minutes <= MaxWakeMinutes
	}
	return false
}

// PR is the open pull request a branch heads, as GitHub reports it. Only humans need it, so it
// comes from GitHub when the page is viewed, never from the bus.
type PR struct {
	Number int    `json:"number"`
	Draft  bool   `json:"draft"`
	URL    string `json:"url"`
}
