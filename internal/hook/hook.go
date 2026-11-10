// Package hook is the Claude Code hook. After each tool call and each prompt it asks the daemon
// for messages this session hasn't been told about, and adds one line per message (sender and
// subject) to the agent's context, with how many are unread; or, where the page's setting says to,
// each message whole, as its turn would read (#204). Before and after each
// tool call that could run a command it also tells the daemon, which finds the commits the call
// made (#21). Those calls and prompts tell the daemon the session is at work. At the end of a turn
// (Stop) the hook asks for what the session was told of and hasn't read, and anything that came
// during the turn, and if there is any it keeps the turn going with it, as each message's turn
// would read; if not, the session is idle (#202).
//
// Pi has no hooks of this kind, so Murmur's Pi extension runs this one (`mur hook --agent pi`) at
// the same moments, with a payload of Claude Code's shape whose session_id is the Pi session's
// file, and reads its answer as Claude Code would (internal/join/pi.go, #205).
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
	// Interrupted is Murmur's own, on a Stop from Pi's extension: the work ended without a chance
	// to go on (Escape, an error), so the hook tells the daemon the turn ended and keeps nothing
	// going. Claude Code fires no Stop then.
	Interrupted bool `json:"interrupted"`

	// native is the session's ID for an agent other than Claude Code (RunFor): Pi's session file.
	native string
}

// pid is the Claude Code process the hook runs under; 0 for another agent's hook, whose
// environment may have one it doesn't run under.
func (in input) pid() int {
	if in.native != "" {
		return 0
	}
	return claudePID()
}

type output struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// stopOutput is a Stop hook's answer that keeps the turn going: Claude Code doesn't stop, and gives
// the agent reason as what to do next.
type stopOutput struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
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

// KnownAgent says RunFor knows the agent named: "" (Claude Code) or "pi".
func KnownAgent(agent string) bool { return agent == "" || agent == "pi" }

// Run reads a hook payload from stdin and writes the hook's answer, if any, to stdout.
func Run(stdin io.Reader, stdout io.Writer) { RunFor("", stdin, stdout) }

// RunFor is Run for the agent named: "" is Claude Code, and "pi" is Murmur's Pi extension. It
// does nothing for any other name, rather than take another agent's hook for Claude Code's.
func RunFor(agent string, stdin io.Reader, stdout io.Writer) {
	defer func() { recover() }()
	if !KnownAgent(agent) {
		return
	}
	var in input
	if err := json.NewDecoder(io.LimitReader(stdin, 1<<20)).Decode(&in); err != nil {
		return
	}
	if in.SessionID == "" || !events[in.Event] {
		return
	}
	if agent == "pi" {
		in.native, in.SessionID = in.SessionID, ""
		// The session is named by its file, never by a Claude Code ID or process: a Pi started from
		// a Claude Code session's shell inherits that session's CLAUDE_PID (see pid). Nothing of
		// Pi's is a transcript of Claude Code's form, and no call is bracketed, so Pi's commits
		// aren't credited to it.
		in.TranscriptPath, in.ToolUseID, in.AgentID = "", "", ""
	} else {
		in.Interrupted = false
	}
	if in.Event == stop {
		turnEnd(in, stdout)
		return
	}
	op := bracketOp(in)
	// Neither PreToolUse nor PostToolUseFailure gives notices: the next PostToolUse or prompt
	// does, and a PreToolUse prints nothing at all.
	wantNotices := in.Event != preTool && in.Event != postFailure
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
			c.Do(ctx, proto.Request{Op: op, ClaudeSession: in.SessionID, Native: in.native, ClientPID: in.pid(),
				ToolUseID: in.ToolUseID, Cwd: in.Cwd, GitDir: gitDir, Subagent: in.AgentID != ""})
		}()
	}
	var resp proto.Response
	if wantNotices {
		nctx, ncancel := context.WithTimeout(ctx, NoticeTimeout)
		resp, err = c.Do(nctx, proto.Request{Op: proto.OpNotices, ClaudeSession: in.SessionID, Native: in.native, ClientPID: in.pid(),
			Transcript: in.TranscriptPath, Subagent: in.AgentID != "", Whole: true})
		ncancel()
	}
	wg.Wait()
	if !wantNotices || err != nil {
		return
	}
	text := view.Hook(resp.Read, resp.Messages, resp.Unread)
	if text == "" {
		return
	}
	var out output
	out.HookSpecificOutput.HookEventName = in.Event
	out.HookSpecificOutput.AdditionalContext = text
	json.NewEncoder(stdout).Encode(out)
}

// turnEnd tells the daemon the turn ended, and keeps it going with the messages the daemon gives
// back, if any, unless it was interrupted: then the daemon gives none back. They are read once the
// daemon answers: a hook that gave up waiting (NoticeTimeout) just before the answer came loses
// them, the risk #204's whole messages take too.
func turnEnd(in input, stdout io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), NoticeTimeout)
	defer cancel()
	gitDir, err := findClone(ctx, in.Cwd)
	if err != nil {
		return
	}
	c, err := client.Connect(ctx, gitDir, false)
	if err != nil {
		return
	}
	resp, err := c.Do(ctx, proto.Request{Op: proto.OpTurnEnd, ClaudeSession: in.SessionID, Native: in.native, ClientPID: in.pid(),
		Subagent: in.AgentID != "", Continues: !in.Interrupted})
	if err != nil || len(resp.Read) == 0 || in.Interrupted {
		return
	}
	json.NewEncoder(stdout).Encode(stopOutput{Decision: "block", Reason: view.Hook(resp.Read, nil, 0)})
}
