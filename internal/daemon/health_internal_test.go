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

// A push is counted by the ref's exact shape, so the deletes of the junk refs that block the bus's
// own (clearOwnJunk) count as other, not as pushes of the device's log or presence.
func TestPushKindIsTheRefsExactShape(t *testing.T) {
	for ref, want := range map[string]int{
		"refs/murmur/mesh/dev1":       refMesh,
		"refs/murmur/presence/dev1":   refPresence,
		"refs/murmur/names":           refNames,
		"refs/murmur/mesh/dev1/x":     refOther,
		"refs/murmur/presence/dev1/x": refOther,
		"refs/murmur/names/x":         refOther,
		"refs/murmur/mesh":            refOther,
		"refs/murmur/presence":        refOther,
		"refs/murmur/presence/":       refOther,
	} {
		if got := kindOf(ref); got != want {
			t.Errorf("kindOf(%q) = %s, want %s", ref, kindNames[got], kindNames[want])
		}
	}
}

// A failed fetch's next try is when it will be: a minute after an ordinary failure, the time set
// for a slow ref, and the next sync after a stale lock was cleared.
func TestRefHealthSaysWhenItIsTriedAgain(t *testing.T) {
	failed := time.Now()
	if got := refHealth("refs/murmur/mesh/dev1", fetchFail{sha: "a", at: failed}); !got.Next.Equal(failed.Add(fetchRetry)) || got.Kind != "mesh" {
		t.Errorf("an ordinary failure: %+v, want the next try at %v", got, failed.Add(fetchRetry))
	}
	later := failed.Add(8 * time.Minute)
	if got := refHealth("refs/murmur/presence/dev1", fetchFail{sha: "a", at: later, slow: true, tries: 3}); !got.Next.Equal(later) || !got.Slow || got.Tries != 3 {
		t.Errorf("a slow ref: %+v, want the next try at %v", got, later)
	}
	if got := refHealth("refs/murmur/mesh/dev1", fetchFail{sha: "a"}); !got.Next.IsZero() {
		t.Errorf("a ref to try at the next sync: %+v", got)
	}
}

// A refusal because the ref moved is counted, and leaves the ref's last error as it was: on the
// names registry it is routine, and would hide the server's refusal that is the trouble.
func TestALeaseRefusalKeepsTheLastError(t *testing.T) {
	var h health
	ctx := context.Background()
	h.pushed(ctx, "refs/murmur/names", fmt.Errorf("push to refs/murmur/names %w: !\tx\t[remote rejected] (failed)", bus.ErrRefused))
	refused := h.pushes[refNames].LastError
	h.pushed(ctx, "refs/murmur/names", bus.ErrRejected)
	p := h.pushes[refNames]
	if p.LeaseRefused != 1 || p.Refused != 1 || p.LastError != refused || refused == nil {
		t.Errorf("after a server refusal and a lease refusal: %+v, last error %+v", p, p.LastError)
	}
}
