package checkin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRepoKey(t *testing.T) {
	for _, url := range []string{
		"https://github.com/a-Fig/murmur.git",
		"https://github.com/a-Fig/murmur",
		"https://user:secret@GitHub.com/a-Fig/murmur.git/",
		"git@github.com:a-Fig/murmur.git",
		"ssh://git@github.com/a-Fig/murmur.git",
		"ssh://git@github.com:22/a-Fig/murmur",
	} {
		if got := RepoKey(url); got != "github.com/a-fig/murmur" {
			t.Errorf("RepoKey(%q) = %q", url, got)
		}
	}
	if got := RepoKey(`C:\repos\team.git`); got != "c:/repos/team" {
		t.Errorf("a Windows path: %q", got)
	}
}

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"0.2.2", "0.2.2", 0}, {"0.2.3", "0.2.2", 1}, {"0.10.0", "0.9.9", 1}, {"1.0.0", "0.99.0", 1},
		{"0.2.2", "0.2.2-pre.1", 1}, {"0.2.2-pre.1", "0.2.2", -1}, {"0.2.2", "0.2.2-dev", 1}, {"0.2.1", "0.2.2-dev", -1},
	} {
		if got := compare(c.a, c.b); got != c.want {
			t.Errorf("compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// clone is a repository with a remote, as a joined clone has, and its git dir.
func clone(t *testing.T, url string) string {
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", dir}, {"-C", dir, "config", "remote.origin.url", url}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return filepath.Join(dir, ".git")
}

type server struct {
	*httptest.Server
	mu  sync.Mutex
	got []Request
}

func newServer(t *testing.T, latest string) *server {
	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding a check-in: %v", err)
		}
		s.mu.Lock()
		s.got = append(s.got, req)
		s.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"latest": latest})
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.got...)
}

func TestOneCheckInADay(t *testing.T) {
	srv := newServer(t, "v0.3.0")
	gitDir := clone(t, "git@github.com:a-Fig/murmur.git")
	now := time.Date(2026, 11, 2, 23, 0, 0, 0, time.UTC)
	o := Options{Version: "0.2.2", GitDir: gitDir, URL: srv.URL, ConfigDir: t.TempDir(),
		Getenv: func(string) string { return "" }, Now: func() time.Time { return now }, OS: "linux", Arch: "arm64"}
	o.defaults()
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := checkInIfDue(ctx, o); err != nil {
			t.Fatal(err)
		}
	}
	got := srv.requests()
	if len(got) != 1 {
		t.Fatalf("%d check-ins on one day, want 1", len(got))
	}
	r := got[0]
	if !validHex(r.User, 32) || !validHex(r.Team, 32) || r.Version != "0.2.2" || r.OS != "linux" || r.Arch != "arm64" {
		t.Errorf("check-in %+v", r)
	}
	if Newer(gitDir, "0.2.2") != "0.3.0" || Newer(gitDir, "0.3.0") != "" {
		t.Errorf("Newer: %q, %q", Newer(gitDir, "0.2.2"), Newer(gitDir, "0.3.0"))
	}

	// The next UTC day, the same user checks in again; another clone of the same repo, cloned
	// another way, is the same team.
	now = now.Add(2 * time.Hour)
	if err := checkInIfDue(ctx, o); err != nil {
		t.Fatal(err)
	}
	o.GitDir = clone(t, "https://github.com/a-Fig/murmur")
	if err := checkInIfDue(ctx, o); err != nil {
		t.Fatal(err)
	}
	got = srv.requests()
	if len(got) != 3 {
		t.Fatalf("%d check-ins, want 3", len(got))
	}
	for _, r := range got[1:] {
		if r.User != got[0].User || r.Team != got[0].Team {
			t.Errorf("check-in %+v, want user %s and team %s", r, got[0].User, got[0].Team)
		}
	}
	o.GitDir = clone(t, "https://github.com/a-Fig/other")
	if err := checkInIfDue(ctx, o); err != nil {
		t.Fatal(err)
	}
	if got = srv.requests(); got[3].Team == got[0].Team {
		t.Error("another repo has the same team")
	}
}

func TestNoCheckIn(t *testing.T) {
	srv := newServer(t, "0.3.0")
	ctx := context.Background()
	base := func() Options {
		return Options{Version: "0.2.2", GitDir: clone(t, "https://github.com/a/b"), URL: srv.URL, ConfigDir: t.TempDir(),
			Getenv: func(string) string { return "" }}
	}
	for name, set := range map[string]func(*Options){
		"DO_NOT_TRACK": func(o *Options) {
			o.Getenv = func(k string) string { return map[string]string{"DO_NOT_TRACK": "1"}[k] }
		},
		"MURMUR_NO_USAGE": func(o *Options) {
			o.Getenv = func(k string) string { return map[string]string{"MURMUR_NO_USAGE": "true"}[k] }
		},
		"git config": func(o *Options) {
			if out, err := exec.Command("git", "--git-dir="+o.GitDir, "config", "murmur.usage", "off").CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
		},
	} {
		o := base()
		set(&o)
		if off, _ := Off(ctx, o.Getenv, o.GitDir); !off {
			t.Errorf("%s: not off", name)
		}
		o.defaults()
		if err := checkInIfDue(ctx, o); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A development build doesn't check in at all.
	o := base()
	o.Version = "0.1.0-dev"
	ctx2, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	Run(ctx2, o)
	if n := len(srv.requests()); n != 0 {
		t.Errorf("%d check-ins, want none", n)
	}
	// DO_NOT_TRACK=0 is not a request to be left out.
	o = base()
	o.Getenv = func(k string) string { return map[string]string{"DO_NOT_TRACK": "0"}[k] }
	if off, _ := Off(ctx, o.Getenv, o.GitDir); off {
		t.Error("DO_NOT_TRACK=0 turned check-ins off")
	}
}

func TestUserIDIsKept(t *testing.T) {
	dir := t.TempDir()
	a, err := userID(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := userID(dir)
	if err != nil || a != b {
		t.Fatalf("second ID %q (%v), first %q", b, err, a)
	}
}

// Daemons for several clones can start at once; they must all end up with one ID.
func TestUserIDConcurrent(t *testing.T) {
	dir := t.TempDir()
	ids := make([]string, 16)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := userID(dir)
			if err != nil {
				t.Error(err)
			}
			ids[i] = id
		}()
	}
	wg.Wait()
	for _, id := range ids[1:] {
		if id != ids[0] {
			t.Fatalf("IDs differ: %q", ids)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d files left in the ID's folder, want 1", len(entries))
	}
}

func TestUserIDReplacesJunk(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "id"), []byte("not an id"), 0o600)
	id, err := userID(dir)
	if err != nil || !validHex(id, 32) {
		t.Fatalf("userID = %q, %v", id, err)
	}
	if again, _ := userID(dir); again != id {
		t.Errorf("second ID %q, first %q", again, id)
	}
}
