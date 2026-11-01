package join

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/client"
)

// Env is the user's machine as join sees it: where it writes, which of the agent programs are
// installed, and which binary the agents should run. EnvFromProcess reads the real one; tests build
// theirs over temporary directories, so none of them can touch the real user's settings.
type Env struct {
	Exe        string // this binary, by absolute path: the agents run it as the MCP server
	ClaudeDir  string // Claude Code's config directory, where its settings.json is
	ClaudeJSON string // the file Claude Code keeps user-scope MCP servers in
	CodexHome  string // Codex's home, where its config.toml is
	Getenv     func(string) string
	LookPath   func(string) (string, error)
}

// EnvFromProcess is the environment of the running process.
func EnvFromProcess() (Env, error) {
	exe, err := os.Executable()
	if err != nil {
		return Env{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	e := Env{Exe: exe, Getenv: os.Getenv, LookPath: exec.LookPath}
	// Claude Code puts everything under CLAUDE_CONFIG_DIR when it is set (a second account, a T3
	// provider with its own home); else its settings are in ~/.claude and its state in ~/.claude.json.
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		e.ClaudeDir, e.ClaudeJSON = dir, filepath.Join(dir, ".claude.json")
	} else {
		e.ClaudeDir, e.ClaudeJSON = filepath.Join(home, ".claude"), filepath.Join(home, ".claude.json")
	}
	if e.CodexHome = os.Getenv("CODEX_HOME"); e.CodexHome == "" {
		e.CodexHome = filepath.Join(home, ".codex")
	}
	return e, nil
}

func (e Env) look(name string) (string, error) {
	if e.LookPath == nil {
		return "", exec.ErrNotFound
	}
	return e.LookPath(name)
}

func (e Env) getenv(key string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(key)
}

// Report is what a join or a leave did and what the agent that ran it should pass on.
type Report struct {
	Did      []string // what changed
	Tell     []string // what the agent tells its user, one paragraph each
	Problems []string // what didn't work; the command fails if there are any
}

// String is the text the command prints.
func (r Report) String() string {
	var b strings.Builder
	if len(r.Did) > 0 {
		b.WriteString("Done:\n")
		for _, d := range r.Did {
			b.WriteString("  - " + d + "\n")
		}
	}
	if len(r.Problems) > 0 {
		b.WriteString("\nProblems:\n")
		for _, p := range r.Problems {
			b.WriteString("  - " + p + "\n")
		}
	}
	if len(r.Tell) > 0 {
		b.WriteString("\nTell your user:\n")
		for _, t := range r.Tell {
			b.WriteString("  " + t + "\n")
		}
	}
	return b.String()
}

// Join does what a user's yes to joining means for the clone containing dir. It is safe to run
// again, and a rerun only fills in what is missing.
func Join(ctx context.Context, e Env, dir string) (Report, error) {
	var r Report
	p, err := Locate(ctx, dir)
	if err != nil {
		return r, err
	}
	if !p.Marked() {
		return r, fmt.Errorf("this repo doesn't say it uses %[1]s: neither AGENTS.md nor CLAUDE.md has the %[1]s section. "+
			"A teammate adds it with `%[2]s init` and commits it; to try it alone, run `%[2]s init` here", brand.Product, brand.Command)
	}
	problem := func(what string, err error) { r.Problems = append(r.Problems, what+": "+err.Error()) }

	// Per user: the MCP server, registered once for every repo this user opens.
	did, claudeFresh, err := e.registerClaude(ctx)
	r.Did = append(r.Did, did...)
	if err != nil {
		problem("Claude Code", err)
	}
	did, codexFresh, err := e.registerCodex()
	r.Did = append(r.Did, did...)
	if err != nil {
		problem("Codex", err)
	}

	// Per repo: the hook that shows incoming messages, in every working tree of the clone.
	trees, err := installHooks(ctx, p, e.Exe)
	for _, t := range trees {
		r.Did = append(r.Did, "Installed the hook in "+filepath.Join(t, filepath.FromSlash(hookFile))+" (not committed)")
	}
	if err != nil {
		problem("hook", err)
	}

	// Consent comes before the daemon: the daemon refuses to run in a clone that hasn't joined.
	if news, err := p.RecordConsent(); err != nil {
		problem("recording that you joined", err)
	} else if news {
		r.Did = append(r.Did, "Recorded that you joined "+brand.Product+" in this clone ("+p.consentPath()+")")
	}
	// A daemon left running by an older mur answers, but can't do what this one does (deliver a message
	// as a turn, for one): it is replaced, which Connect does for a client that may start daemons.
	c, err := client.Connect(ctx, p.GitDir, false)
	switch {
	case errors.Is(err, client.ErrNoDaemon):
		if _, err := client.Connect(ctx, p.GitDir, true); err != nil {
			problem("starting the daemon", err)
		} else {
			r.Did = append(r.Did, "Started this clone's daemon")
		}
	case err == nil && c.Outdated():
		if _, err := client.Connect(ctx, p.GitDir, true); err != nil {
			problem("replacing the daemon of an older version", err)
		} else {
			r.Did = append(r.Did, "Replaced this clone's daemon, which was left running by an older version of "+brand.Product)
		}
	}

	r.Tell = e.advice(claudeFresh, codexFresh, len(r.Problems) > 0)
	if len(r.Did) == 0 && len(r.Problems) == 0 {
		r.Did = []string{"Nothing to do: this clone had already joined " + brand.Product}
	}
	if len(r.Problems) > 0 {
		return r, errors.New("joined, but not everything worked; see above")
	}
	return r, nil
}

// Leave undoes a join for the clone containing dir: the consent, the hook in each working
// tree, and the daemon. The MCP server stays registered with the agents, which is per user, and does
// nothing in a repo that hasn't joined.
func Leave(ctx context.Context, e Env, dir string) (Report, error) {
	var r Report
	p, err := Locate(ctx, dir)
	if err != nil {
		return r, err
	}
	problem := func(what string, err error) { r.Problems = append(r.Problems, what+": "+err.Error()) }

	// The consent first, so everything that checks it stops from here on, including a server that
	// would start the daemon again the moment it is stopped.
	if had, err := p.ForgetConsent(); err != nil {
		problem("removing the record that you joined", err)
	} else if had {
		r.Did = append(r.Did, "Removed the record that you joined "+brand.Product+" in this clone")
	}
	trees, err := removeHooks(ctx, p)
	for _, t := range trees {
		r.Did = append(r.Did, "Removed the hook from "+filepath.Join(t, filepath.FromSlash(hookFile)))
	}
	if err != nil {
		problem("hook", err)
	}
	stopped, stopErr := client.Stop(ctx, p.GitDir)
	if stopErr != nil {
		problem("stopping the daemon", stopErr)
	}
	// A daemon that is still starting has no daemon.json to stop; it sees the consent gone and exits
	// by itself, which is waited for here so a leave doesn't return while one is about to publish.
	starting, err := client.WaitGone(ctx, p.GitDir, 20*time.Second)
	if err != nil {
		problem("waiting for the daemon to exit", fmt.Errorf("%w; it has no consent left and refuses requests, and stops by itself", err))
	}
	if stopped || starting {
		if stopErr == nil && err == nil {
			r.Did = append(r.Did, "Stopped this clone's daemon")
		}
	}

	if len(r.Did) == 0 && len(r.Problems) == 0 {
		r.Did = []string{"Nothing to undo: this clone hadn't joined " + brand.Product}
	}
	r.Tell = []string{
		brand.Product + " is off for this repo. Agents already open here lose its tools within seconds in Claude Code; in Codex the tools stay listed until the session ends, and say they are off if called.",
		"The " + brand.Product + " MCP server stays registered with the agents, since that is per user; it does nothing in a repo that hasn't joined. " +
			"To remove it: `claude mcp remove --scope user " + brand.MCPServer + "`, and delete the [mcp_servers." + brand.MCPServer + "] table from " + filepath.Join(e.CodexHome, "config.toml") + ".",
	}
	if len(r.Problems) > 0 {
		return r, errors.New("not everything worked; see above")
	}
	return r, nil
}

// advice is what to tell the user after a join, by what this machine and this agent need. Which
// agent is running join decides it: the MCP server is registered once, so a session that started
// after that already runs it (idle, with no tools, until the join), and one that started before
// has never heard of it. What each kind of session picks up was measured, 2026-10-29
// (docs/VISION.md, "Reloading MCP servers").
func (e Env) advice(claudeFresh, codexFresh, problems bool) []string {
	if problems {
		return []string{"Joining didn't finish: tell your user what is under Problems. Running `" + brand.Command + " join` again retries, and only does what is missing."}
	}
	var out []string
	switch {
	case e.getenv("CLAUDECODE") == "1" && e.getenv("T3CODE_CLI_PATH") != "" && claudeFresh:
		out = append(out, brand.Product+" was just added to Claude Code, and this conversation started before that, so it doesn't have the tools yet. "+
			"To load them: press Stop in T3, then send any message (T3 restarts Claude with "+brand.Product+" loaded); starting a new thread works too.")
	case e.getenv("CLAUDECODE") == "1" && claudeFresh:
		out = append(out, brand.Product+" was just added to Claude Code, and this conversation started before that, so it doesn't have the tools yet. "+
			"To load them: exit, and run `claude --continue` in this repo, which resumes this conversation with "+brand.Product+" loaded.")
	case e.getenv("CLAUDECODE") == "1":
		out = append(out, brand.Product+" is on for this repo. Its tools (mcp__"+brand.MCPServer+"__who, status, send, inbox) show up in this conversation "+
			"within a few seconds. If they haven't after a minute, start a new conversation.")
	case e.looksLikeCodex() && e.getenv("T3CODE_CLI_PATH") != "":
		// T3's Codex provider loads the server in a thread created after it was registered, and
		// never in an older one, which T3 doesn't refresh (docs/VISION.md, "Reloading MCP servers").
		out = append(out, brand.Product+" is on for this repo, but Codex loads MCP servers only in a thread that starts after they were added, so this thread doesn't have the tools: "+
			"open a new thread in this project to talk to other agents through "+brand.Product+".")
	case e.looksLikeCodex():
		out = append(out, brand.Product+" is on for this repo. Codex loads MCP servers only when a session starts, so this session doesn't have the tools: "+
			"start a new session in this repo to get them.")
	default:
		out = append(out, brand.Product+" is on for this repo. Claude Code sessions pick up its tools within seconds if "+brand.Product+" was already registered with Claude Code, "+
			"and otherwise after a restart (press Stop and send a message in T3; `claude --continue` in a terminal). "+
			"Codex loads them only in a new thread or session.")
	}
	if codexFresh && !e.looksLikeCodex() {
		out = append(out, "Codex sessions already open won't have it; ones started from now on will.")
	}
	return append(out, "They can run `"+brand.Command+" ui` to see the agents and messages on a page. To stop, `"+brand.Command+" join --leave` in this repo.")
}

// looksLikeCodex reports whether the agent running join is Codex. The variables are the ones Codex
// is believed to set for the commands it runs; no Codex session's environment has been checked
// (docs/VISION.md, "Reloading MCP servers"), so this may miss one, and advice then falls back to
// covering every client.
func (e Env) looksLikeCodex() bool {
	for _, k := range []string{"CODEX_THREAD_ID", "CODEX_SANDBOX", "CODEX_CI"} {
		if e.getenv(k) != "" {
			return true
		}
	}
	return false
}
