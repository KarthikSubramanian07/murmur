// Package hook is the Claude Code hook. After each tool call and each prompt it asks the daemon
// for messages this session hasn't been told about, and adds one line per message (sender and
// subject) to the agent's context, with how many are unread; or, where the page's setting says to,
// each message whole, as its turn would read (#204). Before and after each
// tool call that could run a command it also tells the daemon, which finds the commits the call
// made (#21). Those calls and prompts tell the daemon the session is at work, and the end of a
// turn (Stop) that it isn't: a message goes in as a turn only then (#202).
//
// It never blocks or fails the agent: any problem, including no daemon running, means it
// prints nothing and exits 0. Before a tool call it never prints anything at all, and the time it
// can hold the call up is bounded by BracketTimeout.
package hook

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/a-Fig/murmur/internal/client"
	"github.com/a-Fig/murmur/internal/gitx"
	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/view"
)

// input is the part of Claude Code's hook payload the hook reads.
type input struct {
	SessionID string `json:"session_id"`
	// TranscriptPath is the file Claude Code keeps the session's conversation in, which also holds
	// its title.
	TranscriptPath string `json:"transcript_path"`
	Event          string `json:"hook_event_name"`
	Cwd            string `json:"cwd"`
	ToolName       string `json:"tool_name"`
	// ToolUseID is on PreToolUse, PostToolUse and PostToolUseFailure alike, and the same for one
	// call: it is what pairs the two ends (measured on Claude Code 2.1.312, 2026-10-29).
	ToolUseID string `json:"tool_use_id"`
	// AgentID is set on the hooks of a subagent's tool calls, which carry the parent's session_id
	// (Claude Code 2.1.312, 2026-10-29). What the hook prints for one goes to the subagent.
	AgentID string `json:"agent_id"`
}

type output struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// Events the hook answers.
const (
	preTool     = "PreToolUse"
	postTool    = "PostToolUse"
	postFailure = "PostToolUseFailure"
	stop        = "Stop"
)

// events are those the hook acts on. A failing tool call fires PostToolUseFailure, not
// PostToolUse, so a `git commit && git push` whose push failed still has its end seen.
var events = map[string]bool{preTool: true, postTool: true, postFailure: true, stop: true, "UserPromptSubmit": true, "SessionStart": true}

// NoticeTimeout bounds the hook when it only fetches notices: it runs after the call, and a daemon
// that answers slowly is not worth waiting on.
//
// NoticeTimeout and BracketTimeout are variables so that tests on a loaded machine can raise them.
var NoticeTimeout = 3 * time.Second

// BracketTimeout bounds a hook that brackets a tool call. The snapshot at the start has to be taken
// before the call runs, so PreToolUse waits for it; this is the most a call can be held up, and a
// snapshot that misses it just means no commits are credited for that call. The daemon's own limit
// on a snapshot (Options.SnapshotTimeout) is a little shorter.
var BracketTimeout = 5 * time.Second

// cannotRun are the tools that can't run a command, and so can't make a commit. The hook skips
// bracketing them: a snapshot costs git processes, and these are the commonest calls. A tool not
// named here is bracketed, which is the safe way round for one this list has never heard of.
var cannotRun = map[string]bool{
	"Read": true, "Glob": true, "Grep": true, "LS": true,
	"Edit": true, "MultiEdit": true, "Write": true, "NotebookEdit": true,
	"WebFetch": true, "WebSearch": true,
	"TodoWrite": true, "ToolSearch": true, "AskUserQuestion": true,
	"EnterPlanMode": true, "ExitPlanMode": true,
}

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

// bracketOp is the daemon op that tells it about the call this event is the start or end of, or ""
// if the event isn't one, or the call is of a tool that can't commit.
func bracketOp(in input) string {
	if in.ToolUseID == "" || cannotRun[in.ToolName] {
		return ""
	}
	switch in.Event {
	case preTool:
		return proto.OpToolStart
	case postTool, postFailure:
		return proto.OpToolEnd
	}
	return ""
}

// findClone is the git common dir of the repository the hook runs in, which is what names its
// daemon. It is read from the repository's files where it can be (gitx.Discover): a git process
// here would add a tenth of a second or more to every tool call on Windows. Git is asked where it
// can't.
func findClone(ctx context.Context, cwd string) (string, error) {
	if _, common, ok := gitx.Discover(cwd); ok {
		return common, nil
	}
	return client.FindGitDir(ctx, cwd)
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
	op := bracketOp(in)
	if in.Event == stop {
		// The end of a turn only tells the daemon; it prints nothing.
		op = proto.OpTurnEnd
	}
	// Neither PreToolUse nor PostToolUseFailure delivers notices: the next PostToolUse or prompt
	// does, and a PreToolUse prints nothing at all.
	wantNotices := in.Event != preTool && in.Event != postFailure && in.Event != stop
	if !wantNotices && op == "" {
		return
	}
	timeout := NoticeTimeout
	if op != "" {
		timeout = BracketTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	gitDir, err := findClone(ctx, in.Cwd)
	if err != nil {
		return
	}
	c, err := client.Connect(ctx, gitDir, false)
	if err != nil {
		return
	}
	// The end of a call is sent alongside the notices request. The daemon answers it at once and
	// finds the call's commits after, so neither holds up the notices or the agent.
	var wg sync.WaitGroup
	if op != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { recover() }()
			c.Do(ctx, proto.Request{Op: op, ClaudeSession: in.SessionID, ClientPID: claudePID(),
				ToolUseID: in.ToolUseID, Cwd: in.Cwd, GitDir: gitDir, Subagent: in.AgentID != ""})
		}()
	}
	var resp proto.Response
	if wantNotices {
		nctx, ncancel := context.WithTimeout(ctx, NoticeTimeout)
		resp, err = c.Do(nctx, proto.Request{Op: proto.OpNotices, ClaudeSession: in.SessionID, ClientPID: claudePID(),
			Transcript: in.TranscriptPath, Subagent: in.AgentID != ""})
		ncancel()
	}
	wg.Wait()
	if !wantNotices || err != nil {
		return
	}
	text := view.Hook(resp.Delivered, resp.Messages, resp.Unread)
	if text == "" {
		return
	}
	var out output
	out.HookSpecificOutput.HookEventName = in.Event
	out.HookSpecificOutput.AdditionalContext = text
	json.NewEncoder(stdout).Encode(out)
}
