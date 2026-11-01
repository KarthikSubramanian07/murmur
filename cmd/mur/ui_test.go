package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/join"
	"github.com/a-Fig/murmur/internal/setup"
)

// `mur ui` starts the clone's daemon, prints the page's address with the token in its fragment, and
// hands the same address to the browser; --no-open leaves the browser alone.
func TestUIPrintsAndOpensThePage(t *testing.T) {
	_, clone := cloneOf(t, t.TempDir())
	t.Cleanup(func() { stopDaemon(clone) })

	// The user joined in this clone, which is what lets a daemon run in it.
	ctx := context.Background()
	if _, err := setup.Init(ctx, clone); err != nil {
		t.Fatal(err)
	}
	p, err := join.Locate(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.RecordConsent(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	var opened []string
	open := func(u string) error { opened = append(opened, u); return nil }
	if err := runUI(ctx, []string{"-C", clone}, &out, open); err != nil {
		t.Fatal(err)
	}
	address := strings.TrimSpace(out.String())
	if len(opened) != 1 || opened[0] != address {
		t.Fatalf("printed %q and opened %q", address, opened)
	}
	u, err := url.Parse(address)
	token, ok := strings.CutPrefix(u.Fragment, "token=")
	if err != nil || u.Scheme != "http" || !strings.HasPrefix(u.Host, "127.0.0.1:") || u.Path != "/" || !ok || token == "" {
		t.Fatalf("the address %q should be http://127.0.0.1:port/#token=...", address)
	}
	if strings.HasSuffix(u.Host, ":8080") {
		t.Errorf("the page is on %s", u.Host)
	}

	// It is the daemon's page, answering with the token the address carries.
	req, _ := http.NewRequest("GET", "http://"+u.Host+"/api/state", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st struct {
		Device string `json:"device"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil || resp.StatusCode != http.StatusOK || st.Device == "" {
		t.Fatalf("GET /api/state: %d, device %q, %v", resp.StatusCode, st.Device, err)
	}

	// Again, with --no-open: the same address, and no browser.
	out.Reset()
	opened = nil
	if err := runUI(ctx, []string{"-C", clone, "--no-open"}, &out, open); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != address || len(opened) != 0 {
		t.Fatalf("with --no-open: printed %q, opened %q; want %q and nothing opened", got, opened, address)
	}
}

// The page is the daemon's, and a daemon runs only for a user who joined in the clone: without that,
// `mur ui` says so and starts nothing.
func TestUIWithoutJoiningStartsNoDaemon(t *testing.T) {
	_, clone := cloneOf(t, t.TempDir())
	t.Cleanup(func() { stopDaemon(clone) })
	opened := false
	err := runUI(context.Background(), []string{"-C", clone}, &bytes.Buffer{}, func(string) error { opened = true; return nil })
	if err == nil || !strings.Contains(err.Error(), "hasn't joined") {
		t.Fatalf("got %v, want an error saying the clone hasn't joined", err)
	}
	if opened || exists(filepath.Join(clone, ".git", "murmur")) {
		t.Error("a daemon was started, or a browser opened, for a clone that hasn't joined")
	}
}
