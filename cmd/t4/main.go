// Command t4 is the whole tool in one binary: the per-clone daemon, the stdio MCP server each
// agent session starts, the Claude Code hook, and a CLI for humans.
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
	"github.com/a-Fig/t4/internal/mcpserver"
	"github.com/a-Fig/t4/internal/proto"
	"github.com/a-Fig/t4/internal/setup"
	"github.com/a-Fig/t4/internal/view"
)

var version = "0.1.0-dev"

var usage = strings.ReplaceAll(`t4: coding agents on teammates' machines, in the same repo, finding each other.

Setup:
  t4 init                       write .mcp.json, the Claude Code hook and an AGENTS.md section here

For humans (each starts the clone's daemon if it isn't running):
  t4 who                        devices, sessions and what they're doing
  t4 lookup [-n N] <name>       all that's known of one session: name or device/session; N caps its
                                recent messages
  t4 send <to> <subject> [body] to is a session's name, device/session, a device, or '*'; body "-" reads stdin
  t4 inbox                      read messages sent to this device's CLI
  t4 log [-n N]                 recent events on the bus
  t4 ui [--no-open]             open the page showing sessions and messages in a browser; --no-open
                                only prints its address
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
	case "who", "lookup", "send", "inbox", "log", "stop":
		err = runClient(ctx, cmd, args)
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
	fmt.Printf("\nReview and commit these files so teammates' agents get them too. Each teammate needs %q on PATH.\n"+
		"Claude Code asks once whether to trust the project's MCP server.\n", brand.Command)
	return nil
}

func runClient(ctx context.Context, cmd string, args []string) error {
	fs, dir := flags(cmd)
	limit := fs.Int("n", 30, "log: how many events; lookup: how many messages (default 10)")
	fs.Parse(args)
	rest := fs.Args()

	gitDir, err := client.FindGitDir(ctx, *dir)
	if err != nil {
		return err
	}
	c, err := client.Connect(ctx, gitDir, cmd != "stop")
	if errors.Is(err, client.ErrNoDaemon) {
		fmt.Println("No daemon is running for this clone.")
		return nil
	}
	if err != nil {
		return err
	}

	var req proto.Request
	var render func(proto.Response) string
	switch cmd {
	case "who":
		req, render = proto.Request{Op: proto.OpWho}, view.Who
	case "lookup":
		if len(rest) != 1 {
			return errors.New("usage: lookup [-n N] <name or device/session>")
		}
		req, render = proto.Request{Op: proto.OpLookup, Name: rest[0]}, view.Lookup
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "n" {
				req.Limit = *limit
			}
		})
	case "send":
		if len(rest) < 2 {
			return errors.New("usage: send <to> <subject> [body]")
		}
		req = proto.Request{Op: proto.OpSend, To: rest[0], Subject: rest[1], Body: strings.Join(rest[2:], " ")}
		if req.Body == "-" {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			req.Body = string(b)
		}
		render = view.Sent
	case "inbox":
		req, render = proto.Request{Op: proto.OpInbox}, view.Inbox
	case "log":
		req, render = proto.Request{Op: proto.OpLog, Limit: *limit}, view.Log
	case "stop":
		req, render = proto.Request{Op: proto.OpStop}, view.Note
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resp, err := c.Do(ctx, req)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	fmt.Print(render(resp))
	return nil
}
