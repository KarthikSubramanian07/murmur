package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/gitx"
	"github.com/a-Fig/murmur/internal/web"
)

// A failure is shown as a class and one line, with no credential in it: what git says of a remote
// can carry the URL as the user wrote it, token and all.
func TestHealthErrorIsClassedAndClean(t *testing.T) {
	const token = "ghp_0123456789abcdefghijklmnopqrstuvwxyzAB"
	gitErr := func(stderr string) error {
		return &gitx.Error{Args: []string{"ls-remote", "https://x-access-token:" + token + "@github.com/a/b.git"},
			Stderr: stderr, Err: errors.New("exit status 128")}
	}
	for _, c := range []struct {
		name  string
		err   error
		late  bool
		class string
		line  string // what the message must say, "" to skip
	}{
		{"dns", gitErr("fatal: unable to access 'https://github.com/a/b.git/': Could not resolve host: github.com\n"),
			false, web.ErrDNS, "fatal: unable to access 'https://github.com/a/b.git/': Could not resolve host: github.com"},
		{"dns over ssh", gitErr("ssh: Could not resolve hostname github.com: nodename nor servname provided, or not known\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n"),
			false, web.ErrDNS, "fatal: Could not read from remote repository."},
		{"auth over https", gitErr("remote: Invalid username or token.\nfatal: Authentication failed for 'https://github.com/a/b.git/'\n"),
			false, web.ErrAuth, "fatal: Authentication failed for 'https://github.com/a/b.git/'"},
		{"auth over ssh", gitErr("git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.\n"),
			false, web.ErrAuth, ""},
		{"rate limited", gitErr("remote: API rate limit exceeded\nfatal: unable to access 'https://github.com/a/b.git/': The requested URL returned error: 429\n"),
			false, web.ErrRateLimited, ""},
		{"lease", fmt.Errorf("pushing: %w", bus.ErrRejected), false, web.ErrRejected, ""},
		{"refused", fmt.Errorf("push to refs/murmur/presence/x %w: !\trefs/murmur/presence/x\t[remote rejected] (pre-receive hook declined)", bus.ErrRefused),
			false, web.ErrRejected, "[remote rejected] (pre-receive hook declined)"},
		{"timeout", gitErr("error: RPC failed; curl 28 Operation timed out after 300000 milliseconds\n"), false, web.ErrTimeout, ""},
		{"deadline", gitErr(""), true, web.ErrTimeout, ""},
		{"wrapped deadline", fmt.Errorf("reading the names registry and presence took over 15s: %w", context.DeadlineExceeded),
			false, web.ErrTimeout, "reading the names registry and presence took over 15s: context deadline exceeded"},
		{"gone", gitErr("fatal: '/tmp/remote.git' does not appear to be a git repository\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n"),
			false, web.ErrOther, "fatal: '/tmp/remote.git' does not appear to be a git repository"},
		{"credentials in the message", gitErr("fatal: unable to access 'https://x-access-token:" + token + "@github.com/a/b.git/': Failed to connect to github.com port 443\n"),
			false, web.ErrOther, "fatal: unable to access 'https://github.com/a/b.git/': Failed to connect to github.com port 443"},
		{"a secret outside a URL", errors.New("push failed: token=" + token), false, web.ErrOther, "(not shown: it looked like it held a secret)"},
		// The 200-character cut falls inside the token: too short to know for what it is, once cut.
		{"a secret across the cut", errors.New("fatal: " + strings.Repeat("x", 175) + " " + token), false, web.ErrOther,
			"(not shown: it looked like it held a secret)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			if c.late {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			}
			e := healthError(ctx, c.err, time.Now())
			if e.Class != c.class {
				t.Errorf("class %q, want %q (message %q)", e.Class, c.class, e.Message)
			}
			if c.line != "" && !strings.Contains(e.Message, c.line) {
				t.Errorf("message %q, want it to say %q", e.Message, c.line)
			}
			if strings.Contains(e.Message, token[:8]) || strings.Contains(e.Message, "x-access-token") {
				t.Errorf("message %q has the credentials", e.Message)
			}
			if strings.ContainsAny(e.Message, "\n\r") || len([]rune(e.Message)) > maxErrorLine {
				t.Errorf("message %q isn't one short line", e.Message)
			}
		})
	}
}

// A sync or a push that ends because the daemon is shutting down, or its caller gave up, says
// nothing about the remote and isn't counted.
func TestHealthIgnoresCancelled(t *testing.T) {
	var h health
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.synced(ctx, context.Canceled)
	h.pushed(ctx, "refs/murmur/presence/x", context.Canceled)
	h.handedOff(viaT3, context.Canceled)
	if h.syncs != 0 || h.syncFailed != 0 || h.pushes[refPresence] != (web.PushHealth{}) || h.handoffs[0] != (web.HandoffHealth{}) {
		t.Errorf("a cancelled sync, push or handoff was counted: %d syncs, %+v, %+v", h.syncs, h.pushes[refPresence], h.handoffs[0])
	}
	h.synced(context.Background(), errors.New("boom"))
	h.synced(context.Background(), errors.New("boom"))
	if h.failures != 2 || h.failingSince.IsZero() || h.syncFailed != 2 {
		t.Errorf("two failures: %d in a row since %v, %d in all", h.failures, h.failingSince, h.syncFailed)
	}
	h.synced(context.Background(), nil)
	if h.failures != 0 || !h.failingSince.IsZero() || h.syncErr == nil || h.syncs != 3 {
		t.Errorf("after a sync that worked: %d in a row since %v, last error %v, %d syncs", h.failures, h.failingSince, h.syncErr, h.syncs)
	}
}
