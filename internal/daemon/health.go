package daemon

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/gitx"
	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/publicrepo"
	"github.com/a-Fig/murmur/internal/scrub"
	"github.com/a-Fig/murmur/internal/update"
	"github.com/a-Fig/murmur/internal/web"
)

// This device's health, for the page (GET /api/health). A device whose sync with the remote had
// failed showed no sign of it anywhere: the loops wrote a line to daemon.log when they began to fail
// and one when they recovered, and a person learned of a 52-minute DNS outage and of refused pushes
// by reading that log (#141, #87). The snapshot is filled from what the loops already keep, and from
// a few counts kept where things happen. It never leaves the device.

// health is what the daemon counts for the snapshot. Its mu is the last lock taken: it is taken
// alone or under the daemon's mu, and nothing else is taken while it is held.
type health struct {
	mu sync.Mutex
	// syncs and syncFailed count the syncs tried and failed; failures is how many in a row have,
	// since failingSince. syncErr is the last sync's failure, kept after one works.
	syncs, syncFailed int64
	failures          int
	failingSince      time.Time
	syncErr           *web.HealthError
	pushes            [refKinds]web.PushHealth
	giveUps           int64
	handoffs          [2]web.HandoffHealth // by route: viaT3, then viaSocket
	hook              web.HookHealth
	// poll and every are the poll's rate as pollLoop last set it, and the wait it chose.
	poll  string
	every time.Duration
	// remoteURL is the remote's URL for the public repo's state, read at most once a minute
	// (urlRead) so the page's polling seldom runs git.
	remoteURL string
	urlRead   time.Time
}

// The kinds of ref this device pushes to, which the counts are kept by.
const (
	refMesh = iota
	refPresence
	refNames
	refOther
	refKinds
)

// kindOf is the kind of ref by its exact shape: a device's log or presence is the prefix and one
// name. A ref below one, such as presence/<device>/x, is junk that clearOwnJunk deletes, and its
// delete counts as other, not as a push of the device's presence.
func kindOf(ref string) int {
	device := func(prefix string) bool {
		name, ok := strings.CutPrefix(ref, prefix)
		return ok && name != "" && !strings.Contains(name, "/")
	}
	switch {
	case device(brand.MeshPrefix):
		return refMesh
	case device(brand.PresencePrefix):
		return refPresence
	case ref == brand.NamesRef:
		return refNames
	}
	return refOther
}

var kindNames = [refKinds]string{"mesh", "presence", "names", "other"}

// cancelled says a failure came from the caller giving up (the daemon shutting down, a request
// gone), which says nothing about the remote. A deadline that ran out does: it is a timeout.
func cancelled(ctx context.Context, err error) bool {
	return err != nil && errors.Is(ctx.Err(), context.Canceled)
}

// synced records how a sync went. ctx is the one it ran on.
func (h *health) synced(ctx context.Context, err error) {
	if cancelled(ctx, err) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.syncs++
	if err == nil {
		h.failures, h.failingSince = 0, time.Time{}
		return
	}
	now := time.Now()
	h.syncFailed++
	if h.failures == 0 {
		h.failingSince = now
	}
	h.failures++
	h.syncErr = healthError(ctx, err, now)
}

// pushed records how a push to ref went. ctx is the one it ran on.
func (h *health) pushed(ctx context.Context, ref string, err error) {
	if cancelled(ctx, err) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	p := &h.pushes[kindOf(ref)]
	switch {
	case err == nil:
		p.Pushed++
		return
	case errors.Is(err, bus.ErrRejected):
		p.LeaseRefused++
	case errors.Is(err, bus.ErrRefused):
		p.Refused++
	default:
		p.Failed++
	}
	p.LastError = healthError(ctx, err, time.Now())
}

// gaveUp records a backlog given up on.
func (h *health) gaveUp() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.giveUps++
}

// handedOff records a handoff of a message as a turn through via, viaT3 or viaSocket. A failed one
// leaves the message to the hook's notice.
func (h *health) handedOff(via string, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	i := 0
	if via == viaSocket {
		i = 1
	} else if via != viaT3 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.handoffs[i].Tried++
	if err != nil {
		h.handoffs[i].Failed++
	} else {
		h.handoffs[i].Handed++
	}
}

// outOfInbox records a message gone to a session, as outOfInboxLocked words it: "gave" is the
// hook's whole message. A handoff ("handed") is counted by handedOff.
func (h *health) outOfInbox(verb string) {
	if verb != "gave" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hook.Whole++
}

// noticed records n notices the hook showed.
func (h *health) noticed(n int) {
	if n == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hook.Notices += int64(n)
}

// polling records the poll's rate, and the wait to the next look, which is longer while syncs fail.
func (h *health) polling(rate pollRate, wait, base time.Duration) {
	poll := web.PollSlow
	switch {
	case rate.quiet:
		poll = web.PollQuiet
	case rate.every <= base:
		poll = web.PollFast
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.poll, h.every = poll, wait
}

// healthError reduces err to what the page may show (see errorLine).
func healthError(ctx context.Context, err error, at time.Time) *web.HealthError {
	text := err.Error()
	var g *gitx.Error
	if errors.As(err, &g) && strings.TrimSpace(g.Stderr) != "" {
		text = g.Stderr // what git said, without the command line
	}
	class := errorClass(err, text, errors.Is(ctx.Err(), context.DeadlineExceeded))
	return &web.HealthError{Class: class, Message: errorLine(text), At: at}
}

// errorClass sorts a failure into one of web's Err* classes, by what git and the remote said. Git's
// messages for these have stayed the same across its versions; under a locale that translates
// them the class can come out as other, and the message still says what happened.
func errorClass(err error, text string, timedOut bool) string {
	t := strings.ToLower(text)
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(t, s) {
				return true
			}
		}
		return false
	}
	switch {
	case errors.Is(err, bus.ErrRejected) || errors.Is(err, bus.ErrRefused) ||
		has("[remote rejected]", "[rejected]", "pre-receive hook declined", "protected branch"):
		return web.ErrRejected
	case has("rate limit", "too many requests", "error: 429"):
		return web.ErrRateLimited
	case has("could not resolve host", "could not resolve hostname", "name or service not known",
		"nodename nor servname", "temporary failure in name resolution", "no such host", "no address associated"):
		return web.ErrDNS
	case timedOut || errors.Is(err, context.DeadlineExceeded) ||
		has("timed out", "timeout", "operation too slow"):
		return web.ErrTimeout
	case has("authentication failed", "permission denied (publickey", "permission to ", "could not read username",
		"could not read password", "invalid username or password", "terminal prompts disabled", "error: 401",
		"error: 403", "host key verification failed", "repository not found"):
		return web.ErrAuth
	}
	return web.ErrOther
}

// credentials is the user, and password or token, in a URL: what comes between "scheme://" and
// the "@" before the host.
var credentials = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^\s/@'"]*@`)

// maxErrorLine is how long the line shown of an error may be.
const maxErrorLine = 200

// errorLine is the line of text that says most about a failure: git's "fatal:" or "error:" line if
// it has one, else its first. Credentials in URLs are taken out, and a line that still looks like it
// holds a secret (internal/scrub) is not shown at all: the class says what kind of failure it was.
func errorLine(text string) string {
	line := ""
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if line == "" {
			line = l
		}
		if strings.HasPrefix(l, "fatal:") || strings.HasPrefix(l, "error:") {
			line = l
			break
		}
	}
	// Scrubbed whole, before the cut: a token cut in two would be too short for scrub to know it,
	// and its first part would be shown.
	line = credentials.ReplaceAllString(line, "$1")
	if scrub.Check(line) != nil {
		return "(not shown: it looked like it held a secret)"
	}
	return bus.OneLine(line, maxErrorLine)
}

// Health is this device's health, from what the daemon holds. It reads one small file and, at most
// once a minute, the remote's URL from git config; nothing reaches the network.
func (s pageSource) Health(ctx context.Context) (web.Health, error) {
	d := s.d
	version := ""
	if d.opt.Version != nil {
		version = d.opt.Version().Running
	}
	url := d.health.remote(ctx, d.repo)
	repo, addr, looked := publicrepo.Status(d.opt.GitDir, url, version)
	checked := update.Checked(d.opt.GitDir)

	now := time.Now()
	out := web.Health{At: now,
		Daemon:     web.DaemonHealth{Started: d.started, Version: version, Protocol: proto.Protocol, UpdateChecked: checked},
		PublicRepo: web.PublicRepoHealth{State: repo, Repo: addr, Looked: looked},
		Fetches:    web.FetchHealth{Refs: []web.RefHealth{}},
	}
	d.mu.Lock()
	d.pageSeenLocked(ctx, now)
	out.Sync.LastOK, out.Sync.LastOKAge = d.lastPollOK, -1
	if !d.lastPollOK.IsZero() {
		out.Sync.LastOKAge = int64(now.Sub(d.lastPollOK) / time.Second)
	}
	for _, ref := range sortedKeys(d.unfetched) {
		f := d.unfetched[ref]
		out.Fetches.Refs = append(out.Fetches.Refs, web.RefHealth{Ref: ref, Kind: kindNames[kindOf(ref)],
			Slow: f.slow, Tries: f.tries, Landed: f.landed, Next: f.at})
	}
	d.mu.Unlock()

	h := &d.health
	h.mu.Lock()
	defer h.mu.Unlock()
	out.Sync.Syncs, out.Sync.Failed = h.syncs, h.syncFailed
	out.Sync.FailingSince, out.Sync.Failures, out.Sync.LastError = h.failingSince, h.failures, h.syncErr
	out.Pushes = web.PushesHealth{Mesh: h.pushes[refMesh], Presence: h.pushes[refPresence],
		Names: h.pushes[refNames], Other: h.pushes[refOther]}
	out.Fetches.BacklogGiveUps = h.giveUps
	out.Delivery = web.DeliveryHealth{T3: h.handoffs[0], Socket: h.handoffs[1], Hook: h.hook}
	out.Daemon.Poll, out.Daemon.PollEvery = h.poll, h.every.Seconds()
	return out, nil
}

// remote is the remote's URL, read again once it is a minute old. No lock is held while git runs.
func (h *health) remote(ctx context.Context, repo *bus.Repo) string {
	h.mu.Lock()
	url, read := h.remoteURL, h.urlRead
	h.mu.Unlock()
	if !read.IsZero() && time.Since(read) < time.Minute {
		return url
	}
	url = repo.Config(ctx, "remote."+repo.Remote+".url")
	if ctx.Err() != nil {
		return url // a read cut short says nothing of the URL; the next call reads it again
	}
	h.mu.Lock()
	h.remoteURL, h.urlRead = url, time.Now()
	h.mu.Unlock()
	return url
}
