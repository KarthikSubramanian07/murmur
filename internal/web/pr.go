package web

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// LookupPR finds the open pull request a branch heads: nil, nil if there is none.
type LookupPR func(ctx context.Context, branch string) (*PR, error)

// GH looks pull requests up with the gh CLI, run in dir so it finds the repository from the
// clone's remotes and uses the user's own GitHub login.
func GH(dir string) LookupPR {
	return func(ctx context.Context, branch string) (*PR, error) {
		// --head=<branch>, in one argument, so no branch name can pass for a flag.
		cmd := exec.CommandContext(ctx, "gh", "pr", "list", "--head="+branch, "--state", "open",
			"--json", "number,isDraft,url")
		cmd.Dir = dir
		// The daemon runs detached, with nobody to answer a prompt or read an update notice.
		cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1")
		cmd.WaitDelay = 2 * time.Second
		hideWindow(cmd)
		out, err := cmd.Output()
		if err != nil {
			return nil, err
		}
		var prs []struct {
			Number  int    `json:"number"`
			IsDraft bool   `json:"isDraft"`
			URL     string `json:"url"`
		}
		if err := json.Unmarshal(out, &prs); err != nil {
			return nil, fmt.Errorf("gh pr list: %w", err)
		}
		if len(prs) == 0 {
			return nil, nil
		}
		return &PR{Number: prs[0].Number, Draft: prs[0].IsDraft, URL: prs[0].URL}, nil
	}
}

const (
	prTTL      = time.Minute      // how long a lookup's answer is shown before it is redone
	prForget   = 10 * time.Minute // a branch no session has shown for this long is dropped
	prTimeout  = 15 * time.Second // one lookup, however slow GitHub is
	prParallel = 4                // lookups at once, so ten new branches don't start ten gh's
	maxBranch  = 255              // longer than git allows; such a branch isn't looked up
)

// prCache answers PR state from memory and refreshes it in the background, so a page poll never
// waits on GitHub. A failed lookup counts as no PR until the next one.
type prCache struct {
	lookup LookupPR
	sem    chan struct{}

	mu      sync.Mutex
	entries map[string]*prEntry
}

type prEntry struct {
	pr       *PR
	fetched  time.Time // zero until the first lookup ends
	asked    time.Time // when a page last showed the branch
	inflight bool
}

func newPRCache(lookup LookupPR) *prCache {
	return &prCache{lookup: lookup, sem: make(chan struct{}, prParallel), entries: map[string]*prEntry{}}
}

// get returns what is known of each branch's PR, leaving out branches with none or not yet
// looked up, and starts a lookup for each branch whose answer is missing or old.
func (c *prCache) get(branches []string) map[string]PR {
	now := time.Now()
	out := map[string]PR{}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, b := range branches {
		if !lookupable(b) {
			continue
		}
		e := c.entries[b]
		if e == nil {
			e = &prEntry{}
			c.entries[b] = e
		}
		e.asked = now
		if e.pr != nil {
			out[b] = *e.pr
		}
		if !e.inflight && now.Sub(e.fetched) >= prTTL {
			e.inflight = true
			go c.refresh(b)
		}
	}
	for b, e := range c.entries {
		if !e.inflight && now.Sub(e.asked) > prForget {
			delete(c.entries, b)
		}
	}
	return out
}

func (c *prCache) refresh(branch string) {
	c.sem <- struct{}{}
	defer func() { <-c.sem }()
	ctx, cancel := context.WithTimeout(context.Background(), prTimeout)
	defer cancel()
	pr, err := c.lookup(ctx, branch)
	if err != nil {
		pr = nil // gh missing, logged out, offline: show no PR state rather than an error
	}
	if pr != nil && !strings.HasPrefix(pr.URL, "https://") {
		pr.URL = ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[branch]
	if e == nil {
		e = &prEntry{asked: time.Now()}
		c.entries[branch] = e
	}
	e.pr, e.fetched, e.inflight = pr, time.Now(), false
}

func lookupable(branch string) bool {
	if branch == "" || len(branch) > maxBranch || strings.HasPrefix(branch, "-") {
		return false
	}
	for _, r := range branch {
		if r < 0x20 || r == 0x7f || r == ' ' {
			return false
		}
	}
	return true
}
