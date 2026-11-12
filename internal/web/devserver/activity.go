package main

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/a-Fig/murmur/internal/web"
)

// Sample activity for the Recent view (#448): what a daemon would have seen of the fixture
// sessions over the last few hours, and what GitHub would say of the repository.

func fixtureActivity(now time.Time) []web.Activity {
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	acts := []web.Activity{
		{Kind: web.ActStarted, At: ago(3 * time.Hour), Who: "solar-wren", Agent: "codex", Branch: "wake-ups"},
		{Kind: web.ActStarted, At: ago(80 * time.Minute), Who: "lucid-badger", Agent: "claude-code", Branch: "session-names"},
		{Kind: web.ActStatus, At: ago(78 * time.Minute), Who: "lucid-badger", Doing: "Reading how names are assigned"},
		{Kind: web.ActCommits, At: ago(70 * time.Minute), Who: "brave-heron", Branch: "remove-claims", Commits: 1, Subject: "Take claims out of the MCP tools"},
		{Kind: web.ActCommits, At: ago(66 * time.Minute), Who: "brave-heron", Branch: "remove-claims", Commits: 2, Subject: "Take claims out of the daemon"},
		{Kind: web.ActCommits, At: ago(61 * time.Minute), Who: "brave-heron", Branch: "remove-claims", Commits: 1, Subject: "Drop the claims tests"},
		{Kind: web.ActStatus, At: ago(58 * time.Minute), Who: "lucid-badger", Doing: "Trying compare-and-swap on a ref"},
		{Kind: web.ActStatus, At: ago(55 * time.Minute), Who: "lucid-badger", Doing: "Taking names by compare-and-swap on refs/murmur/names"},
		{Kind: web.ActStarted, At: ago(52 * time.Minute), Who: "quiet-otter", Agent: "claude-code", Branch: "web-page"},
		{Kind: web.ActTags, At: ago(50 * time.Minute), Who: "quiet-otter", Added: []string{"ui"}},
		{Kind: web.ActStopped, At: ago(47 * time.Minute), Who: "keen-pike"},
		{Kind: web.ActCommits, At: ago(44 * time.Minute), Who: "solar-wren", Branch: "wake-ups"},
		{Kind: web.ActStarted, At: ago(35 * time.Minute), Who: "velvet-moth", Agent: "t3", Branch: "session-names"},
		{Kind: web.ActTags, At: ago(31 * time.Minute), Who: "lucid-badger", Added: []string{"reviewing"}, Removed: []string{"do-not-disturb"}},
		{Kind: web.ActStarted, At: ago(25 * time.Minute), Who: "rapid-ibis", Agent: "claude-code", Branch: "fix/ls-remote-rate-limit"},
		{Kind: web.ActStatus, At: ago(24 * time.Minute), Who: "rapid-ibis", Doing: "Measuring ls-remote against GitHub's limits"},
		{Kind: web.ActCommits, At: ago(12 * time.Minute), Who: "quiet-otter", Branch: "web-page", Commits: 3, Subject: "Lay the settings view out in two columns"},
		{Kind: web.ActStatus, At: ago(6 * time.Minute), Who: "quiet-otter", Doing: "Building the settings view of the web page"},
	}
	for i := range acts {
		acts[i].ID = "act-" + strconv.Itoa(i+1)
	}
	return acts
}

// fixtureGitHub answers as gh would, a little slowly.
func fixtureGitHub(now time.Time) web.LookupGitHub {
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	ev := func(kind, what string, at time.Time, n int, title, branch string) web.GitHubEvent {
		url := "https://github.com/a-Fig/murmur/pull/" + strconv.Itoa(n)
		if kind == web.GHIssue {
			url = "https://github.com/a-Fig/murmur/issues/" + strconv.Itoa(n)
		}
		return web.GitHubEvent{ID: "gh-" + kind + "-" + strconv.Itoa(n) + "-" + what, Kind: kind, What: what, At: at, Number: n,
			Title: title, URL: url, Author: "tyler", Branch: branch}
	}
	events := []web.GitHubEvent{
		ev(web.GHIssue, web.GHOpened, ago(150*time.Minute), 34, "Two sessions can take the same name", ""),
		ev(web.GHPullRequest, web.GHOpened, ago(74*time.Minute), 31, "Take session names by compare-and-swap", "session-names"),
		ev(web.GHPullRequest, web.GHOpened, ago(59*time.Minute), 32, "Remove claims", "remove-claims"),
		ev(web.GHPullRequest, web.GHMerged, ago(40*time.Minute), 32, "Remove claims", "remove-claims"),
		ev(web.GHIssue, web.GHClosed, ago(39*time.Minute), 29, "Claims go stale when a session stops", ""),
		ev(web.GHPullRequest, web.GHOpened, ago(10*time.Minute), 36, "A settings view for the page", "web-page"),
		ev(web.GHIssue, web.GHOpened, ago(4*time.Minute), 37, "ls-remote is rate limited on a busy repo", ""),
	}
	return func(ctx context.Context) ([]web.GitHubEvent, error) {
		select {
		case <-time.After(400 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return events, nil
	}
}

// Activity uses the count of activities seen as its cursor, as Messages does.
func (f *fixtures) Activity(ctx context.Context, cursor string) ([]web.Activity, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	from := 0
	if cursor != "" {
		n, err := strconv.Atoi(cursor)
		if err != nil || n < 0 || n > len(f.activity) {
			return nil, "", errors.New("bad cursor")
		}
		from = n
	}
	return append([]web.Activity(nil), f.activity[from:]...), strconv.Itoa(len(f.activity)), nil
}
