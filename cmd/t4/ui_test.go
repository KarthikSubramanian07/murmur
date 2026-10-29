package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A daemon that a command starts is this binary run as `t4 daemon`, which under test is the test
// binary: let it be one.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		main()
		return
	}
	os.Exit(m.Run())
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// `t4 ui` starts the clone's daemon, prints the page's address with the token in its fragment, and
// hands the same address to the browser; --no-open leaves the browser alone.
func TestUIPrintsAndOpensThePage(t *testing.T) {
	root := t.TempDir()
	remote, seed, clone := filepath.Join(root, "remote.git"), filepath.Join(root, "seed"), filepath.Join(root, "clone")
	git(t, root, "init", "-q", "--bare", "--initial-branch=main", remote)
	git(t, root, "init", "-q", "--initial-branch=main", seed)
	git(t, seed, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, seed, "push", "-q", remote, "HEAD:refs/heads/main")
	git(t, root, "clone", "-q", remote, clone)
	t.Cleanup(func() {
		runClient(context.Background(), "stop", []string{"-C", clone})
		// The daemon removes daemon.json as it goes, and holds the clone's files until then.
		for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
			if _, err := os.Stat(filepath.Join(clone, ".git", "t4", "daemon.json")); err != nil {
				return
			}
		}
	})

	ctx := context.Background()
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
