// Package hook is the Claude Code hook that delivers notices: after each tool call and each
// prompt, it asks the daemon for messages this session hasn't been told about and adds one line
// per message (sender and subject, never the body) to the agent's context.
//
// It never blocks or fails the agent: any problem, including no daemon running, means it
// prints nothing and exits 0.
package hook

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/a-Fig/t4/internal/client"
	"github.com/a-Fig/t4/internal/proto"
	"github.com/a-Fig/t4/internal/view"
)

// input is the part of Claude Code's hook payload the hook reads.
type input struct {
	SessionID string `json:"session_id"`
	Event     string `json:"hook_event_name"`
	Cwd       string `json:"cwd"`
}

type output struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// Events the hook answers. Each can add context to the conversation without steering a turn.
var events = map[string]bool{"PostToolUse": true, "UserPromptSubmit": true, "SessionStart": true}

// claudePID is the Claude Code process this hook runs under, which Claude Code gives hooks as
// CLAUDE_PID; 0 where it isn't set. The MCP server's parent is the same process, which is how the
// daemon knows the hook's session ID is the one that server's session now has.
func claudePID() int {
	pid, err := strconv.Atoi(os.Getenv("CLAUDE_PID"))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// Run reads a hook payload from stdin and writes the hook's answer, if any, to stdout.
func Run(stdin io.Reader, stdout io.Writer) {
	defer func() { recover() }()
	var in input
	if err := json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&in); err != nil {
		return
	}
	if in.SessionID == "" || !events[in.Event] {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	gitDir, err := client.FindGitDir(ctx, in.Cwd)
	if err != nil {
		return
	}
	c, err := client.Connect(ctx, gitDir, false)
	if err != nil {
		return
	}
	resp, err := c.Do(ctx, proto.Request{Op: proto.OpNotices, ClaudeSession: in.SessionID, ClientPID: claudePID()})
	if err != nil || len(resp.Messages) == 0 {
		return
	}
	var out output
	out.HookSpecificOutput.HookEventName = in.Event
	out.HookSpecificOutput.AdditionalContext = view.Notices(resp.Messages)
	json.NewEncoder(stdout).Encode(out)
}
