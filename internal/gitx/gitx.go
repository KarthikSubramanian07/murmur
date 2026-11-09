// Package gitx runs the git binary. Running git, rather than linking a git library, gives Murmur
// each user's credential helpers, SSH config and URL rewrites with no configuration of its own.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/a-Fig/murmur/internal/proc"
)

// Git runs commands against one repository's git directory.
type Git struct {
	// Dir is passed as --git-dir, so no command it runs can read or write a working tree or an
	// index. Humans and agents are editing the working tree at the same moment.
	Dir string
	// Env is added to the environment of every command, such as a GIT_INDEX_FILE of the caller's
	// own, so that building a tree doesn't use the index the working tree's users share.
	Env []string
}

// Error is a failed git command, with what it printed to stderr.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *Error) Unwrap() error { return e.Err }

// Run runs git with the given stdin and returns its stdout.
func (g Git) Run(ctx context.Context, stdin string, args ...string) (string, error) {
	return run(ctx, "", stdin, g.Env, append([]string{"--git-dir=" + g.Dir}, args...)...)
}

// RunIn runs git in a working directory. It is for the few reads that need one, such as finding
// a repository or a session's current branch. The one write that goes through it is `mur init`
// fast-forwarding the checkout it was run in, which a person asked for.
func RunIn(ctx context.Context, dir string, args ...string) (string, error) {
	return run(ctx, dir, "", nil, args...)
}

// startLimit is how long git may take to start (proc.StartWithin). It takes milliseconds: under the
// race detector, with twelve starting at once, never over 0.7 s (#217).
const startLimit = 10 * time.Second

// run runs git once more if its start hung (proc.ErrStartHung). That git never ran, or, started
// late, was killed at once, which a read and the writing of an object (commit-tree) don't mind; a
// push that may have landed is checked by its caller, as after any failed push.
func run(ctx context.Context, dir, stdin string, env []string, args ...string) (string, error) {
	out, err := runOnce(ctx, dir, stdin, env, args...)
	if errors.Is(err, proc.ErrStartHung) && ctx.Err() == nil {
		out, err = runOnce(ctx, dir, stdin, env, args...)
	}
	return out, err
}

func runOnce(ctx context.Context, dir, stdin string, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	// A git killed outright can leave a ref's .lock file behind, and every later write to that
	// ref fails until it is removed. Where the platform allows, interrupt it instead, so git
	// removes its own locks, and kill it only if it hasn't exited soon after.
	if interrupt != nil {
		cmd.Cancel = func() error { return interrupt(cmd.Process) }
	}
	// Without a WaitDelay, Wait also waits for grandchildren (git-remote-https, ssh) that hold
	// the output pipes, so even a killed git could keep its caller blocked.
	cmd.WaitDelay = 5 * time.Second
	cmd.Dir = dir
	// The daemon runs detached, with nobody to answer a prompt: fail instead of waiting.
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never"), env...)
	hideWindow(cmd)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := proc.StartWithin(cmd, startLimit, cancel); err != nil {
		return "", &Error{Args: args, Err: err}
	}
	if err := cmd.Wait(); err != nil {
		return out.String(), &Error{Args: args, Stderr: errb.String(), Err: err}
	}
	return out.String(), nil
}
