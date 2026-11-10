package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type fakeRepos struct {
	list   []Repo
	url    string // what Open answers
	err    error
	opened []string
}

func (f *fakeRepos) List(ctx context.Context) ([]Repo, error) { return f.list, nil }
func (f *fakeRepos) Open(ctx context.Context, id string) (string, error) {
	f.opened = append(f.opened, id)
	return f.url, f.err
}

// startWith is start, with repos and with the server itself to hand.
func startWith(t *testing.T, repos Repos) (*harness, *Server) {
	t.Helper()
	page := NewWithPRs(newFake(), testToken, noPRs)
	if repos != nil {
		page.WithRepos(repos)
	}
	srv := httptest.NewServer(page)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return &harness{t: t, srv: srv, port: u.Port()}, page
}

// trade posts a key to /api/key as the page does, with the given content type.
func (h *harness) trade(key, ctype string) *http.Response {
	h.t.Helper()
	req, _ := http.NewRequest("POST", h.srv.URL+"/api/key", strings.NewReader(`{"key":"`+key+`"}`))
	req.Host = "127.0.0.1:" + h.port
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// A key gets the page's token once, as JSON only, and never after a minute.
func TestAKeyOpensThePageOnce(t *testing.T) {
	h, page := startWith(t, nil)
	key := page.PageKey()
	if len(key) < 64 {
		t.Fatalf("the key %q is short", key)
	}
	// Plain text and forms can come from any site without asking; they don't spend the key.
	for _, ctype := range []string{"", "text/plain", "application/x-www-form-urlencoded"} {
		if resp := h.trade(key, ctype); resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("a key as %q: %d, want 415", ctype, resp.StatusCode)
		}
	}
	if resp := h.trade(key+"0", "application/json"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a wrong key: %d, want 401", resp.StatusCode)
	}
	resp := h.trade(key, "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the key: %d", resp.StatusCode)
	}
	noCORS(t, resp)
	if got := decode[keyResponse](t, resp); got.Token != testToken {
		t.Fatalf("the key gave %q", got.Token)
	}
	if resp := h.trade(key, "application/json"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a spent key: %d, want 401", resp.StatusCode)
	}

	// An old key is no good, and keys don't pile up.
	old := page.PageKey()
	page.keys.mu.Lock()
	page.keys.keys[old] = time.Now().Add(-time.Second)
	page.keys.mu.Unlock()
	if resp := h.trade(old, "application/json"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("an expired key: %d, want 401", resp.StatusCode)
	}
	for range 3 * maxKeys {
		page.PageKey()
	}
	if n := len(page.keys.keys); n > maxKeys {
		t.Errorf("%d keys kept", n)
	}

	// Only the exact path skips the token: no other spelling reaches the trade.
	key = page.PageKey()
	for _, path := range []string{"/api/key/", "/api//key", "/API/key"} {
		req, _ := http.NewRequest("POST", h.srv.URL+path, strings.NewReader(`{"key":"`+key+`"}`))
		req.Host = "127.0.0.1:" + h.port
		req.Header.Set("Content-Type", "application/json")
		resp, err := h.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode/100 == 2 {
			t.Errorf("POST %s: %d", path, resp.StatusCode)
		}
	}
	if resp := h.trade(key, "application/json"); resp.StatusCode != http.StatusOK {
		t.Errorf("the key was spent by another path: %d", resp.StatusCode)
	}
	// Only POST.
	if resp := h.do("GET", "/api/key", "127.0.0.1:"+h.port, "", ""); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/key: %d", resp.StatusCode)
	}
}

// The picker's calls need the token; opening a repo hands the tab only a page's address with a key.
func TestReposListAndOpen(t *testing.T) {
	repos := &fakeRepos{
		list: []Repo{{ID: "a1", Name: "murmur", Path: "/code/murmur", Running: true, Current: true}, {ID: "b2", Name: "site", Path: "/code/site"}},
		url:  "http://127.0.0.1:5123/#key=abc",
	}
	h, _ := startWith(t, repos)
	host := "127.0.0.1:" + h.port
	if resp := h.do("GET", "/api/repos", host, "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /api/repos without the token: %d", resp.StatusCode)
	}
	if resp := h.do("POST", "/api/repos/open", host, "", `{"id":"b2"}`); resp.StatusCode != http.StatusUnauthorized || len(repos.opened) != 0 {
		t.Errorf("POST /api/repos/open without the token: %d, opened %v", resp.StatusCode, repos.opened)
	}
	got := decode[reposResponse](t, h.ok("GET", "/api/repos", ""))
	if len(got.Repos) != 2 || got.Repos[1].Name != "site" {
		t.Errorf("listed %+v", got.Repos)
	}
	if u := decode[openResponse](t, h.ok("POST", "/api/repos/open", `{"id":"b2"}`)); u.URL != repos.url {
		t.Errorf("open gave %q", u.URL)
	}

	repos.err = ErrNoRepo
	if resp := h.do("POST", "/api/repos/open", host, testToken, `{"id":"gone"}`); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a repo that left: %d, want 404", resp.StatusCode)
	}
	repos.err = errors.New("the daemon didn't come up")
	if resp := h.do("POST", "/api/repos/open", host, testToken, `{"id":"b2"}`); resp.StatusCode != http.StatusBadGateway {
		t.Errorf("a daemon that won't start: %d, want 502", resp.StatusCode)
	}
	repos.err = nil
	for _, bad := range []string{"https://evil.example/#key=abc", "http://127.0.0.2:5123/#key=abc", "http://127.0.0.1:5123/#token=abc",
		"http://127.0.0.1:5123/x#key=abc", "javascript:alert(1)", "http://127.0.0.1:5123/#key=abc&token=x"} {
		repos.url = bad
		if resp := h.do("POST", "/api/repos/open", host, testToken, `{"id":"b2"}`); resp.StatusCode != http.StatusBadGateway {
			t.Errorf("open passed on %q: %d", bad, resp.StatusCode)
		}
	}

	// Without repos, the list is empty and nothing opens.
	h2, _ := startWith(t, nil)
	if got := decode[reposResponse](t, h2.ok("GET", "/api/repos", "")); len(got.Repos) != 0 {
		t.Errorf("listed %v with no repos", got.Repos)
	}
	if resp := h2.do("POST", "/api/repos/open", "127.0.0.1:"+h2.port, testToken, `{"id":"b2"}`); resp.StatusCode != http.StatusNotFound {
		t.Errorf("open with no repos: %d", resp.StatusCode)
	}
}
