// Package gitx runs the git binary. Running git, rather than linking a git library, gives Murmur
// each user's credential helpers, SSH config and URL rewrites with no configuration of its own.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Git runs commands against one repository's git directory.
type Git struct {
	// Dir is passed as --git-dir, so no command it runs can read or write a working tree or an
	// index. Humans and agents are editing the working tree at the same moment.
	Dir string
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
	return run(ctx, "", stdin, append([]string{"--git-dir=" + g.Dir}, args...)...)
}

// RunIn runs git in a working directory. It is for the few reads that need one, such as finding
// a repository or a session's current branch; nothing that writes goes through it.
func RunIn(ctx context.Context, dir string, args ...string) (string, error) {
	return run(ctx, dir, "", args...)
}

func run(ctx context.Context, dir, stdin string, args ...string) (string, error) {
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
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
	hideWindow(cmd)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), &Error{Args: args, Stderr: errb.String(), Err: err}
	}
	return out.String(), nil
}
