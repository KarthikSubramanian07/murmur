// Command t4 is the whole tool in one binary: the per-clone daemon, the stdio MCP server each
// agent session starts, the Claude Code hook, and the few commands a person runs: setup, joining
// and the page. Messaging is for agents, through the MCP server, and for humans, through the page.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/a-Fig/t4/internal/brand"
	"github.com/a-Fig/t4/internal/client"
	"github.com/a-Fig/t4/internal/daemon"
	"github.com/a-Fig/t4/internal/hook"
	"github.com/a-Fig/t4/internal/join"
	"github.com/a-Fig/t4/internal/mcpserver"
	"github.com/a-Fig/t4/internal/setup"
)

var version = "0.1.0-dev"

var usage = strings.ReplaceAll(`t4: coding agents on teammates' machines, in the same repo, finding each other.

Setup:
  t4 init                       add the section to AGENTS.md and CLAUDE.md that says this repo uses t4
  t4 join [--leave]             join t4 in this repo, after the user agreed (an agent runs this);
                                --leave undoes it

For humans:
  t4 ui [--no-open]             open the page showing sessions and messages in a browser; --no-open
                                only prints its address (starts the clone's daemon if it isn't running)
  t4 stop                       stop this clone's daemon

Started by other programs:
  t4 mcp [--agent NAME]         stdio MCP server, one per agent session
  t4 hook                       Claude Code hook (reads the hook payload on stdin)
  t4 daemon [--git-dir DIR]     the daemon, in the foreground

Every command takes -C DIR to act on the repository at DIR.
`, "t4", brand.Command)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "hook":
		hook.Run(os.Stdin, os.Stdout)
	case "mcp":
		err = runMCP(ctx, args)
	case "daemon":
		err = runDaemon(ctx, args)
	case "init":
		err = runInit(ctx, args)
	case "join":
		err = runJoin(ctx, args, os.Stdout)
	case "stop":
		err = runStop(ctx, args, os.Stdout)
	case "ui":
		err = runUI(ctx, args, os.Stdout, browse)
	case "version", "--version":
		fmt.Println(brand.Command, version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", brand.Command, err)
		os.Exit(1)
	}
}

func flags(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(brand.Command+" "+name, flag.ExitOnError)
	dir := fs.String("C", ".", "act on the repository at this directory")
	return fs, dir
}

func runMCP(ctx context.Context, args []string) error {
	fs, dir := flags("mcp")
	agent := fs.String("agent", "", "the agent's name as teammates see it (default: detected)")
	fs.Parse(args)
	if err := os.Chdir(*dir); err != nil {
		return err
	}
	// stdout carries the protocol; anything else goes to stderr, which clients log.
	log.SetOutput(os.Stderr)
	return mcpserver.Run(ctx, mcpserver.Options{Agent: *agent, Version: version})
}

func runDaemon(ctx context.Context, args []string) error {
	fs, dir := flags("daemon")
	gitDir := fs.String("git-dir", "", "the clone's git common dir (default: found from -C)")
	poll := fs.Duration("poll", 0, "how often to check the remote (default 10s)")
	idle := fs.Duration("idle", 0, "exit after this long with no sessions (default 10m; negative, e.g. -1s: never)")
	logPath := fs.String("log", "", "append output to this file, not stderr (for starters that can't pass a handle)")
	fs.Parse(args)
	if *logPath != "" {
		if err := redirectOutput(*logPath); err != nil {
			return err
		}
	}
	if *gitDir == "" {
		var err error
		if *gitDir, err = client.FindGitDir(ctx, *dir); err != nil {
			return err
		}
	}
	// A daemon publishes this machine's presence to the team, which only a user who joined in this
	// clone has agreed to. Whoever started this one (a stale client, a script) doesn't change that.
	if !join.Joined(*gitDir) {
		return fmt.Errorf("this clone hasn't joined %[1]s, so its daemon won't run (`%[1]s join` joins, after asking the user)", brand.Command)
	}
	err := daemon.Run(ctx, daemon.Options{GitDir: *gitDir, Poll: *poll, Idle: *idle,
		Log: log.New(os.Stderr, "", log.LstdFlags)})
	if errors.Is(err, daemon.ErrRunning) {
		return nil // another client started one first; that one serves
	}
	return err
}

// redirectOutput sends this process's stdout and stderr to the file, appending. WMI gives a process
// no handles, so a starter that uses it names the log file instead.
func redirectOutput(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	os.Stdout, os.Stderr = f, f
	log.SetOutput(f)
	// A panic trace is written to stderr, which here is the log: no other stderr reaches it.
	return debug.SetCrashOutput(f, debug.CrashOptions{})
}

func runInit(ctx context.Context, args []string) error {
	fs, dir := flags("init")
	fs.Parse(args)
	changed, err := setup.Init(ctx, *dir)
	for _, c := range changed {
		fmt.Println(c)
	}
	if err != nil {
		return err
	}
	if len(changed) == 0 {
		fmt.Println("Already set up; nothing changed.")
		return nil
	}
	fmt.Printf("\nReview and commit these files. A teammate's agent that reads them asks its user whether to join,\n"+
		"and if the user says yes it installs %[1]s and sets it up itself. Committing installs nothing, and %[1]s\n"+
		"does nothing in a clone until its user has joined there.\n", brand.Command)
	return nil
}

func runJoin(ctx context.Context, args []string, out io.Writer) error {
	fs, dir := flags("join")
	leave := fs.Bool("leave", false, "undo joining in this clone: forget the consent, remove the notice hook, stop the daemon")
	fs.Parse(args)
	env, err := join.EnvFromProcess()
	if err != nil {
		return err
	}
	do := join.Join
	if *leave {
		do = join.Leave
	}
	report, err := do(ctx, env, *dir)
	fmt.Fprint(out, report.String())
	return err
}

func runStop(ctx context.Context, args []string, out io.Writer) error {
	fs, dir := flags("stop")
	fs.Parse(args)
	gitDir, err := client.FindGitDir(ctx, *dir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	stopped, err := client.Stop(ctx, gitDir)
	if err != nil {
		return err
	}
	if !stopped {
		fmt.Fprintln(out, "No daemon is running for this clone.")
		return nil
	}
	fmt.Fprintln(out, "Stopped the daemon.")
	return nil
}
