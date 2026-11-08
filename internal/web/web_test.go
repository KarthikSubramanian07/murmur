package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
)

const testToken = "s3cret-token"

type fakeSource struct {
	mu       sync.Mutex
	state    State
	settings Settings
	sets     int
	cursor   string
	unmuted  [][2]string
	tagged   []string // each SetTags: addr +added -removed
	watched  []bool   // what Watched said of each State call
}

func (f *fakeSource) State(ctx context.Context) (State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.watched = append(f.watched, Watched(ctx))
	return f.state, nil
}

func (f *fakeSource) Messages(ctx context.Context, cursor string) ([]Message, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cursor = cursor
	return []Message{{ID: "m1", From: "quiet-otter", To: "*", ToKind: ToAll, Subject: "hi"}}, "c1", nil
}

func (f *fakeSource) Settings(ctx context.Context) (Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settings, nil
}

func (f *fakeSource) SetSettings(ctx context.Context, s Settings) (Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets++
	s.DefaultMinutes = 3
	f.settings = s
	return s, nil
}

func (f *fakeSource) Unmute(ctx context.Context, by, muted string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unmuted = append(f.unmuted, [2]string{by, muted})
	return nil
}

func (f *fakeSource) SetTags(ctx context.Context, addr string, add, remove []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tagged = append(f.tagged, addr+" +"+strings.Join(add, ",")+" -"+strings.Join(remove, ","))
	return nil
}

// seen returns what the source has been handed, under its lock: the server wrote it on another
// goroutine.
func (f *fakeSource) seen() (sets int, cursor string, unmuted [][2]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sets, f.cursor, append([][2]string(nil), f.unmuted...)
}

func newFake() *fakeSource {
	return &fakeSource{
		state: State{Device: "tyler-laptop-3f2a", Devices: []Device{{
			ID: "tyler-laptop-3f2a", Self: true, Reachable: true,
			Sessions: []Session{
				{Name: "quiet-otter", Agent: "claude-code", Branch: "web-page"},
				{Name: "brave-heron", Agent: "codex", Branch: "-x"},
			},
		}}},
		settings: Settings{Wake: Wake{When: WakeWithin}, DefaultMinutes: 3},
	}
}

type harness struct {
	t    *testing.T
	srv  *httptest.Server
	port string
}

func start(t *testing.T, src Source, lookup LookupPR) *harness {
	t.Helper()
	srv := httptest.NewServer(NewWithPRs(src, testToken, lookup))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return &harness{t: t, srv: srv, port: u.Port()}
}

// do sends a request with the given Host header and token ("" for none).
func (h *harness) do(method, path, host, token, body string) *http.Response {
	h.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, h.srv.URL+path, r)
	if err != nil {
		h.t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (h *harness) ok(method, path, body string) *http.Response {
	h.t.Helper()
	resp := h.do(method, path, "127.0.0.1:"+h.port, testToken, body)
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		h.t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, b)
	}
	return resp
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func noPRs(context.Context, string) (*PR, error) { return nil, nil }

func noCORS(t *testing.T, resp *http.Response) {
	t.Helper()
	for k := range resp.Header {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Errorf("%s %s sent %s", resp.Request.Method, resp.Request.URL.Path, k)
		}
	}
}

func TestTokenRequired(t *testing.T) {
	h := start(t, newFake(), noPRs)
	host := "127.0.0.1:" + h.port
	for _, path := range []string{"/api/state", "/api/messages", "/api/settings"} {
		for _, token := range []string{"", "wrong", testToken + "x", strings.ToUpper(testToken)} {
			if resp := h.do("GET", path, host, token, ""); resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("GET %s with token %q: %d, want 401", path, token, resp.StatusCode)
			}
		}
		h.ok("GET", path, "")
	}
	// Writes too, and before the body is looked at.
	if resp := h.do("PUT", "/api/settings", host, "", `{"wake":{"when":"never"}}`); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("PUT /api/settings without token: %d, want 401", resp.StatusCode)
	}
	if resp := h.do("POST", "/api/unmute", host, "", `{"by":"a-b","muted":"c-d"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST /api/unmute without token: %d, want 401", resp.StatusCode)
	}
	// The token goes in the header only: not as a query parameter, not without "Bearer".
	if resp := h.do("GET", "/api/state?token="+testToken, host, "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("token in query: %d, want 401", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", h.srv.URL+"/api/state", nil)
	req.Header.Set("Authorization", testToken)
	if resp, _ := h.srv.Client().Do(req); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("token without Bearer: %d, want 401", resp.StatusCode)
		resp.Body.Close()
	}
	// An empty token configured is no token: nothing gets in.
	empty := httptest.NewServer(NewWithPRs(newFake(), "", noPRs))
	defer empty.Close()
	req, _ = http.NewRequest("GET", empty.URL+"/api/state", nil)
	req.Header.Set("Authorization", "Bearer ")
	if resp, _ := empty.Client().Do(req); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("server with empty token: %d, want 401", resp.StatusCode)
		resp.Body.Close()
	}

	// The page itself loads without the token, so it can read it from the fragment.
	for _, path := range []string{"/", "/assets/app.js", "/assets/style.css", "/assets/theme.js",
		"/assets/icons.js", "/assets/favicon.svg", "/assets/fonts/inter-latin-wght-normal.woff2", "/assets/THIRD_PARTY_NOTICES"} {
		if resp := h.do("GET", path, host, "", ""); resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s without token: %d, want 200", path, resp.StatusCode)
		}
	}
	if resp := h.do("GET", "/assets/", host, "", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /assets/: %d, want 404 (no listing)", resp.StatusCode)
	}
}

func TestNoCORS(t *testing.T) {
	h := start(t, newFake(), noPRs)
	host := "127.0.0.1:" + h.port
	for _, c := range []struct{ method, path, token string }{
		{"GET", "/", ""}, {"GET", "/api/state", testToken}, {"GET", "/api/state", ""},
		{"OPTIONS", "/api/state", ""}, {"OPTIONS", "/api/settings", testToken},
	} {
		req, _ := http.NewRequest(c.method, h.srv.URL+c.path, nil)
		req.Host = host
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("Access-Control-Request-Method", "PUT")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := h.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		noCORS(t, resp)
		resp.Body.Close()
	}
}

func TestHostCheck(t *testing.T) {
	h := start(t, newFake(), noPRs)
	for _, host := range []string{"127.0.0.1:" + h.port, "localhost:" + h.port, "LocalHost:" + h.port} {
		for _, path := range []string{"/", "/api/state"} {
			if resp := h.do("GET", path, host, testToken, ""); resp.StatusCode != http.StatusOK {
				t.Errorf("Host %q, GET %s: %d, want 200", host, path, resp.StatusCode)
			}
		}
	}
	other := "1"
	if h.port == "1" {
		other = "2"
	}
	for _, host := range []string{
		"evil.example:" + h.port, // a rebound name: the browser sends the attacker's host
		"evil.example",
		"127.0.0.1",
		"localhost",
		"127.0.0.1:" + other,
		"127.0.0.1:" + h.port + ".evil.example",
		"[::1]:" + h.port,
		"0.0.0.0:" + h.port,
	} {
		for _, path := range []string{"/", "/assets/app.js", "/api/state"} {
			resp := h.do("GET", path, host, testToken, "")
			if resp.StatusCode != http.StatusMisdirectedRequest {
				t.Errorf("Host %q, GET %s: %d, want 421", host, path, resp.StatusCode)
			}
			if b, _ := io.ReadAll(resp.Body); strings.Contains(string(b), "quiet-otter") {
				t.Errorf("Host %q, GET %s leaked state", host, path)
			}
		}
	}
	// Without a listener's address to compare against, nothing is accepted.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	NewWithPRs(newFake(), testToken, noPRs).ServeHTTP(rec, req)
	if rec.Code != http.StatusMisdirectedRequest {
		t.Errorf("request with no local address: %d, want 421", rec.Code)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	src := newFake()
	h := start(t, src, noPRs)
	want := Settings{
		Wake: Wake{When: WakeWithin, Minutes: 7},
		SessionWake: map[string]Wake{
			"quiet-otter": {When: WakeNever},
			"brave-heron": {When: WakeAlways},
			"amber-finch": {When: WakeWithin},
		},
	}
	body, _ := json.Marshal(want)
	saved := decode[Settings](t, h.ok("PUT", "/api/settings", string(body)))
	want.DefaultMinutes = 3 // reported by the source, whatever was sent
	if !reflect.DeepEqual(saved, want) {
		t.Errorf("PUT returned %+v, want %+v", saved, want)
	}
	got := decode[Settings](t, h.ok("GET", "/api/settings", ""))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GET returned %+v, want %+v", got, want)
	}

	sets, _, _ := src.seen()
	for _, bad := range []string{
		`{"wake":{"when":"sometimes"}}`,
		`{"wake":{"when":"always","minutes":5}}`,
		`{"wake":{"when":"within","minutes":-1}}`,
		`{"wake":{"when":"within","minutes":1441}}`,
		`{"wake":{"when":"never"},"sessionWake":{"quiet-otter":{"when":"maybe"}}}`,
		`{"wake":{"when":"never"},"sessionWake":{"":{"when":"never"}}}`,
		`{"wake":{"when":"never"},"extra":true}`,
		`{"wake":`,
		``,
	} {
		resp := h.do("PUT", "/api/settings", "127.0.0.1:"+h.port, testToken, bad)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("PUT %s: %d, want 400", bad, resp.StatusCode)
		}
	}
	if after, _, _ := src.seen(); after != sets {
		t.Errorf("invalid settings reached the source")
	}
	if got := decode[Settings](t, h.ok("GET", "/api/settings", "")); !reflect.DeepEqual(got, want) {
		t.Errorf("after rejected PUTs, GET returned %+v, want %+v", got, want)
	}
}

func TestMessagesAndUnmute(t *testing.T) {
	src := newFake()
	h := start(t, src, noPRs)
	got := decode[messagesResponse](t, h.ok("GET", "/api/messages?cursor=abc", ""))
	_, cursor, _ := src.seen()
	if cursor != "abc" || got.Cursor != "c1" || len(got.Messages) != 1 || got.Messages[0].ToKind != ToAll {
		t.Errorf("messages: source saw cursor %q, page got %+v", cursor, got)
	}
	if resp := h.do("GET", "/api/messages?cursor="+strings.Repeat("x", 300), "127.0.0.1:"+h.port, testToken, ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("long cursor: %d, want 400", resp.StatusCode)
	}

	h.ok("POST", "/api/unmute", `{"by":"quiet-otter","muted":"dusty-lynx"}`)
	if _, _, unmuted := src.seen(); !reflect.DeepEqual(unmuted, [][2]string{{"quiet-otter", "dusty-lynx"}}) {
		t.Errorf("unmuted %v", unmuted)
	}
	for _, bad := range []string{`{"by":"quiet-otter"}`, `{"muted":"x"}`, `nope`} {
		if resp := h.do("POST", "/api/unmute", "127.0.0.1:"+h.port, testToken, bad); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("unmute %s: %d, want 400", bad, resp.StatusCode)
		}
	}
	if resp := h.do("GET", "/api/unmute", "127.0.0.1:"+h.port, testToken, ""); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/unmute: %d, want 405", resp.StatusCode)
	}
}

func TestIndex(t *testing.T) {
	h := start(t, newFake(), noPRs)
	resp := h.ok("GET", "/", "")
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "<title>"+brand.Product+"</title>") {
		t.Errorf("index doesn't carry the brand name:\n%s", b)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("CSP = %q", csp)
	}
	if rp := resp.Header.Get("Referrer-Policy"); rp != "no-referrer" {
		t.Errorf("Referrer-Policy = %q", rp)
	}
}

// pollPRs fetches state until cond holds for its PRs, or fails after a few seconds.
func pollPRs(t *testing.T, h *harness, cond func(map[string]PR) bool) map[string]PR {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := decode[stateResponse](t, h.ok("GET", "/api/state", ""))
		if cond(st.PRs) {
			return st.PRs
		}
		if time.Now().After(deadline) {
			t.Fatalf("PRs never settled; last %+v", st.PRs)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPRLookup(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	lookup := func(ctx context.Context, branch string) (*PR, error) {
		mu.Lock()
		asked = append(asked, branch)
		mu.Unlock()
		return &PR{Number: 36, Draft: true, URL: "https://github.com/a-Fig/murmur/pull/36"}, nil
	}
	h := start(t, newFake(), lookup)
	prs := pollPRs(t, h, func(p map[string]PR) bool { return len(p) > 0 })
	if want := (PR{Number: 36, Draft: true, URL: "https://github.com/a-Fig/murmur/pull/36"}); prs["web-page"] != want || len(prs) != 1 {
		t.Errorf("prs = %+v", prs)
	}
	// The cached answer serves later polls without asking again, and a branch that could pass
	// for a flag is never asked about.
	for range 5 {
		h.ok("GET", "/api/state", "")
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(asked, []string{"web-page"}) {
		t.Errorf("looked up %v, want [web-page] once", asked)
	}
}

func TestPRLookupFailureTolerated(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	failing := func(ctx context.Context, branch string) (*PR, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return nil, errors.New("gh: not logged in")
	}
	h := start(t, newFake(), failing)
	for range 3 {
		st := decode[stateResponse](t, h.ok("GET", "/api/state", ""))
		if len(st.PRs) != 0 || len(st.Devices) != 1 || len(st.Devices[0].Sessions) != 2 {
			t.Fatalf("state with failing lookups: %+v", st)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := calls
		mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("lookup never ran")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st := decode[stateResponse](t, h.ok("GET", "/api/state", "")); len(st.PRs) != 0 {
		t.Errorf("a failed lookup produced PRs: %+v", st.PRs)
	}
}

// With no gh on PATH, the real lookup fails, and the page still gets its state.
func TestGHMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	pr, err := GH(t.TempDir())(context.Background(), "web-page")
	if err == nil || pr != nil {
		t.Fatalf("GH with no gh on PATH = %v, %v; want an error", pr, err)
	}
	h := start(t, newFake(), GH(t.TempDir()))
	st := decode[stateResponse](t, h.ok("GET", "/api/state", ""))
	if len(st.Devices) != 1 {
		t.Errorf("state = %+v", st)
	}
	time.Sleep(100 * time.Millisecond)
	if st := decode[stateResponse](t, h.ok("GET", "/api/state", "")); len(st.PRs) != 0 {
		t.Errorf("PRs with no gh: %+v", st.PRs)
	}
}

func TestPRURLMustBeHTTPS(t *testing.T) {
	lookup := func(ctx context.Context, branch string) (*PR, error) {
		return &PR{Number: 1, URL: "javascript:alert(1)"}, nil
	}
	h := start(t, newFake(), lookup)
	prs := pollPRs(t, h, func(p map[string]PR) bool { return len(p) > 0 })
	if prs["web-page"].URL != "" {
		t.Errorf("non-https PR URL passed through: %q", prs["web-page"].URL)
	}
}

// A page in a tab nobody can see says so, and the source hears it: a daemon lets only a page
// someone can see keep it running.
func TestHiddenPageIsNotWatched(t *testing.T) {
	src := newFake()
	h := start(t, src, noPRs)
	h.ok("GET", "/api/state", "")
	req, _ := http.NewRequest("GET", h.srv.URL+"/api/state", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("X-Page-Hidden", "1")
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("hidden page's call: %d", resp.StatusCode)
	}
	src.mu.Lock()
	defer src.mu.Unlock()
	if !reflect.DeepEqual(src.watched, []bool{true, false}) {
		t.Errorf("watched = %v, want [true false]: a visible page's call, then a hidden one's", src.watched)
	}
}
