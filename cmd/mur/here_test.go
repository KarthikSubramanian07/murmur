package main

import (
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/here"
)

// Claude Code (100) started OpenCode (200), and OpenCode's command ran join: OpenCode's fixed
// record is the nearest, so join moves nothing, and above all not Claude's session.
func TestJoinFromOpenCodeUnderClaudeMovesNothing(t *testing.T) {
	claude := here.Session{ServerPID: 10, ClientPID: 100, Agent: "claude-code"}
	open := here.Session{ServerPID: 20, ClientPID: 200, Agent: "opencode", Fixed: true}
	mine := here.Caller([]here.Session{claude, open}, []int{7, 200, 100}, 100, "")
	s, ok, why := pickMove(mine)
	if ok || s.ServerPID != 0 || !strings.Contains(why, "can't be moved") {
		t.Errorf("pickMove = %+v, %v, %q; want nothing moved, and why", s, ok, why)
	}
}

func TestPickMove(t *testing.T) {
	a := here.Session{ServerPID: 10, ClientPID: 100}
	if s, ok, why := pickMove([]here.Session{a}); !ok || s != a || why != "" {
		t.Errorf("one session: %+v, %v, %q", s, ok, why)
	}
	if _, ok, why := pickMove(nil); ok || why != "" {
		t.Errorf("no session: %v, %q; want nothing, silently", ok, why)
	}
	if _, ok, why := pickMove([]here.Session{a, {ServerPID: 11, ClientPID: 100}}); ok || why == "" {
		t.Errorf("two sessions: %v, %q; want none, and why", ok, why)
	}
}
