package main

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/client"
	"github.com/a-Fig/murmur/internal/join"
	"github.com/a-Fig/murmur/internal/setup"
)

// `mur ui` starts the clone's daemon, prints the page's address with the token in its fragment, and
// hands the browser a file that leads there; --no-open leaves the browser alone.
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
	open := func(path string) error { opened = append(opened, path); return nil }
	if err := runUI(ctx, []string{"-C", clone}, &out, open); err != nil {
		t.Fatal(err)
	}
	address := strings.TrimSpace(out.String())

	// The browser is given a file in the daemon's state directory, not the address, which would be
	// on its opener's command line; the file leads to the address.
	gitDir, err := client.FindGitDir(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	opener := filepath.Join(client.StateDir(gitDir), openerName)
	if len(opened) != 1 || opened[0] != opener {
		t.Fatalf("printed %q and opened %q; want %q opened", address, opened, opener)
	}
	if info, err := os.Stat(client.StateDir(gitDir)); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Errorf("the state directory's mode is %v; want only its owner let in", info.Mode().Perm())
	}
	page, err := os.ReadFile(opener)
	if err != nil || !strings.Contains(string(page), `location.replace("`+address+`")`) {
		t.Fatalf("the file opened doesn't lead to %q: %v\n%s", address, err, page)
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

// The file the browser opens leads to the address by script, by refresh and by a link, each
// escaped for its place, so an address with quotes and tags in it arrives as it was and breaks out
// of none of them. It asks for no referrer.
func TestOpenerLeadsToTheAddress(t *testing.T) {
	const fragment = `token=a"b<'/script>&c d`
	address := "http://127.0.0.1:5000/#" + fragment
	path, err := writeOpener(t.TempDir(), address)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	if !strings.Contains(page, `<meta name="referrer" content="no-referrer">`) {
		t.Errorf("no referrer policy:\n%s", page)
	}
	if strings.Contains(page, address) || strings.Count(page, "</script>") != 1 {
		t.Fatalf("the address isn't escaped:\n%s", page)
	}

	script := regexp.MustCompile(`<script>location\.replace\((.*)\);</script>`).FindStringSubmatch(page)
	var inScript string
	if script == nil || json.Unmarshal([]byte(script[1]), &inScript) != nil || inScript != address {
		t.Errorf("the script goes to %v, want %q", script, address)
	}
	refresh := regexp.MustCompile(`<meta http-equiv="refresh" content="0; url=([^"]*)">`).FindStringSubmatch(page)
	if refresh == nil || html.UnescapeString(refresh[1]) != address {
		t.Errorf("the refresh goes to %v, want %q", refresh, address)
	}
	link := regexp.MustCompile(`<a href="([^"]*)">`).FindStringSubmatch(page)
	var u *url.URL
	if link != nil {
		u, err = url.Parse(html.UnescapeString(link[1]))
	}
	if link == nil || err != nil || u.Host != "127.0.0.1:5000" || u.Fragment != fragment {
		t.Errorf("the link goes to %v (%v), want %q", link, err, address)
	}
}

// The file replaces whatever was there before, readable by its owner only even if the old one was
// readable by all, and leaves nothing else behind.
func TestOpenerIsPrivate(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, openerName)
	if err := os.WriteFile(old, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := writeOpener(dir, "http://127.0.0.1:5000/#token=new")
	if err != nil || path != old {
		t.Fatalf("wrote %q, %v; want %q", path, err, old)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "token=new") {
		t.Fatalf("the file wasn't replaced: %v\n%s", err, b)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("left %d files, want only %s", len(entries), openerName)
	}
	if runtime.GOOS == "windows" {
		return // permissions there are the directory's ACL, not a mode
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Errorf("the file's mode is %v; want 0600", info.Mode().Perm())
	}
}

// Windows is handed a file URL, which has no spaces for rundll32 to keep the quotes around.
func TestFileURL(t *testing.T) {
	for in, want := range map[string]string{
		"C:/Users/A B/repo/.git/murmur/open-page.html": "file:///C:/Users/A%20B/repo/.git/murmur/open-page.html",
		"/home/a b/repo/.git/murmur/open-page.html":    "file:///home/a%20b/repo/.git/murmur/open-page.html",
	} {
		if got := fileURL(in); got != want {
			t.Errorf("fileURL(%q) = %q, want %q", in, got, want)
		}
	}
}
