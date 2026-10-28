// Package proto is the local protocol between the daemon and its clients (MCP servers, the hook,
// the CLI): one JSON request line and one JSON response line per connection, over loopback TCP.
// A session's hello connection is the exception: it stays open for the session's lifetime, and
// its closing is how the daemon learns the session stopped, even after a crash.
package proto

import "time"

// Ops.
const (
	OpPing    = "ping"
	OpHello   = "hello"
	OpWho     = "who"
	OpStatus  = "status"
	OpClaims  = "claims"
	OpClaim   = "claim"
	OpRelease = "release"
	OpSend    = "send"
	OpInbox   = "inbox"
	OpNotices = "notices"
	OpLog     = "log"
	OpStop    = "stop"
)

// CLISession is the session ID of requests from the command line. It is always present on its
// device, so a human can claim, send and read an inbox without an agent.
const CLISession = "cli"

// Info is what the daemon writes to daemon.json for clients to find it.
type Info struct {
	Addr   string `json:"addr"`
	Token  string `json:"token"`
	PID    int    `json:"pid"`
	Device string `json:"device"`
}

// Request is one call to the daemon.
type Request struct {
	Token   string `json:"token"`
	Op      string `json:"op"`
	Session string `json:"session,omitempty"` // the caller; "" is the CLI

	Hello *Hello `json:"hello,omitempty"`

	To      string   `json:"to,omitempty"`
	Subject string   `json:"subject,omitempty"`
	Body    string   `json:"body,omitempty"`
	Doing   string   `json:"doing,omitempty"`
	What    string   `json:"what,omitempty"`
	Paths   []string `json:"paths,omitempty"`
	Seen    string   `json:"seen,omitempty"`
	Claim   string   `json:"claim,omitempty"`
	Limit   int      `json:"limit,omitempty"`

	// ClaudeSession finds the session for the hook, which only knows Claude Code's ID for it.
	ClaudeSession string `json:"claudeSession,omitempty"`
}

// Hello registers a session.
type Hello struct {
	ID            string `json:"id"`
	Agent         string `json:"agent"`
	Cwd           string `json:"cwd"`
	ClaudeSession string `json:"claudeSession,omitempty"`
	// Reconnect says the session was registered with a daemon that went away, so it should get
	// what that daemon would have delivered.
	Reconnect bool `json:"reconnect,omitempty"`
}

// Response is the daemon's answer. Which fields are set depends on the op.
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Self  string `json:"self,omitempty"` // the caller's address
	Note  string `json:"note,omitempty"`

	Devices []Device `json:"devices,omitempty"`

	Claims  []ClaimView `json:"claims,omitempty"`  // open claims
	Unseen  []ClaimView `json:"unseen,omitempty"`  // claim: open claims the caller's token didn't show
	Claimed *ClaimView  `json:"claimed,omitempty"` // claim: the claim just made
	Seen    string      `json:"seen,omitempty"`    // a token for what these results showed, for claim

	Messages []MessageView `json:"messages,omitempty"`
	Events   []EventView   `json:"events,omitempty"`
}

// Device is one device as `who` shows it.
type Device struct {
	ID       string        `json:"id"`
	Host     string        `json:"host"`
	Owner    string        `json:"owner,omitempty"`
	Self     bool          `json:"self,omitempty"`
	Stale    bool          `json:"stale,omitempty"`
	LastSeen time.Time     `json:"lastSeen"` // when this daemon last saw its presence change
	Sessions []SessionView `json:"sessions"`
}

// SessionView is one session as `who` shows it.
type SessionView struct {
	Addr    string      `json:"addr"`
	Agent   string      `json:"agent"`
	Branch  string      `json:"branch,omitempty"`
	Doing   string      `json:"doing,omitempty"`
	Started time.Time   `json:"started"`
	You     bool        `json:"you,omitempty"`
	Claims  []ClaimView `json:"claims,omitempty"`
}

// ClaimView is one claim.
type ClaimView struct {
	ID    string    `json:"id"`
	By    string    `json:"by"`
	What  string    `json:"what"`
	Paths []string  `json:"paths,omitempty"`
	At    time.Time `json:"at"`
}

// MessageView is one received message. Notices carry no Body.
type MessageView struct {
	ID         string    `json:"id"`
	From       string    `json:"from"`
	FromAgent  string    `json:"fromAgent,omitempty"`
	FromBranch string    `json:"fromBranch,omitempty"`
	To         string    `json:"to"`
	Subject    string    `json:"subject"`
	Body       string    `json:"body,omitempty"`
	At         time.Time `json:"at"`
	Note       bool      `json:"note,omitempty"` // from this device's daemon, about this session
}

// EventView is one line of `log`.
type EventView struct {
	At   time.Time `json:"at"`
	Line string    `json:"line"`
}
