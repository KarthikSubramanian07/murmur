package web

import (
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// Attacks on the page that must fail, from the review of 2026-10-29. The page runs in the human's
// browser, every website they visit can send it requests, and what it shows comes from whoever can
// push to the repo.

// raw sends a request exactly as written, so that paths reach the server uncleaned, as a hostile
// client would send them.
func (h *harness) raw(t *testing.T, request string) (int, string) {
	t.Helper()
	conn, err := net.Dial("tcp", h.srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(conn)
	resp := string(b)
	line, _, _ := strings.Cut(resp, "\r\n") // HTTP/1.1 401 Unauthorized
	fields := strings.Fields(line)
	if len(fields) < 2 {
		t.Fatalf("no status line in %q", resp)
	}
	code, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("no status in %q", line)
	}
	return code, resp
}

// No spelling of an API path gets past the token check: the check looks at the path as the
// request has it, and the router cleans paths, so the two must agree on what is the API.
func TestTokenCheckNotBypassedByPath(t *testing.T) {
	src := newFake()
	h := start(t, src, noPRs)
	host := "127.0.0.1:" + h.port
	body := `{"wake":{"when":"never"}}`
	for _, path := range []string{
		"//api/settings", "/./api/settings", "/assets/../api/settings", "/%61pi/settings",
		"/api/./settings", "/api//settings", "/api/settings/", "/API/settings", "/api%2fsettings",
		"/assets/%2e%2e/api/settings",
	} {
		for _, method := range []string{"PUT", "GET"} {
			req := method + " " + path + " HTTP/1.1\r\nHost: " + host + "\r\nContent-Type: application/json\r\n" +
				"Content-Length: " + strconv.Itoa(len(body)) + "\r\nConnection: close\r\n\r\n" + body
			code, resp := h.raw(t, req)
			if code/100 == 2 {
				t.Errorf("%s %s without the token: %d", method, path, code)
			}
			if strings.Contains(resp, "quiet-otter") || strings.Contains(resp, `"wake"`) {
				t.Errorf("%s %s without the token answered with state:\n%s", method, path, resp)
			}
		}
	}
	if sets, _, _ := src.seen(); sets != 0 {
		t.Errorf("settings were saved %d times without the token", sets)
	}
}

// Another website can make the browser send a "simple" request, a form post or a fetch with no
// custom headers, to the page without asking first. None of them carries the token, so none
// changes anything; a browser's remembered Basic credentials don't stand in for it either.
func TestCrossSiteWritesRefused(t *testing.T) {
	src := newFake()
	h := start(t, src, noPRs)
	for _, c := range []struct{ method, path, ctype, body, auth string }{
		{"POST", "/api/unmute", "text/plain", `{"by":"quiet-otter","muted":"brave-heron"}`, ""},
		{"POST", "/api/unmute", "application/x-www-form-urlencoded", `by=quiet-otter&muted=brave-heron`, ""},
		{"POST", "/api/unmute", "multipart/form-data; boundary=x", "--x--", ""},
		{"PUT", "/api/settings", "text/plain", `{"wake":{"when":"never"}}`, ""},
		{"POST", "/api/settings", "text/plain", `{"wake":{"when":"never"}}`, ""},
		{"PUT", "/api/settings", "application/json", `{"wake":{"when":"never"}}`, "Basic " + testToken},
		{"PUT", "/api/settings", "application/json", `{"wake":{"when":"never"}}`, "bearer " + testToken},
	} {
		req, _ := http.NewRequest(c.method, h.srv.URL+c.path, strings.NewReader(c.body))
		req.Host = "127.0.0.1:" + h.port // the browser was sent to the real address, so Host passes
		req.Header.Set("Content-Type", c.ctype)
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		resp, err := h.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s (%s, auth %q) from another site: %d, want 401", c.method, c.path, c.ctype, c.auth, resp.StatusCode)
		}
		noCORS(t, resp)
	}
	if sets, _, unmuted := src.seen(); sets != 0 || len(unmuted) != 0 {
		t.Errorf("another site changed things: %d saves, unmuted %v", sets, unmuted)
	}
}

// Every answer the page gives, the page, its assets, the API and its errors, refuses to be framed
// by another site, which could otherwise lay the page under its own and steer the human's clicks.
func TestNothingCanBeFramed(t *testing.T) {
	h := start(t, newFake(), noPRs)
	host := "127.0.0.1:" + h.port
	for _, c := range []struct{ path, token string }{
		{"/", ""}, {"/assets/app.js", ""}, {"/api/state", testToken}, {"/api/state", ""}, {"/nowhere", ""},
	} {
		resp := h.do("GET", c.path, host, c.token, "")
		if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("GET %s: CSP %q doesn't forbid framing", c.path, csp)
		}
	}
}

// Text from the bus reaches the page only as JSON, which a browser won't run or render as a page,
// and the page's policy lets no inline script run even if some got in.
func TestHostileTextStaysData(t *testing.T) {
	src := newFake()
	evil := `</script><script>alert(1)</script><img src=x onerror=alert(1)>`
	src.state.Devices[0].Owner = evil
	src.state.Devices[0].Sessions[0].Doing = evil
	src.state.Devices[0].Sessions[0].Branch = evil
	h := start(t, src, noPRs)
	resp := h.ok("GET", "/api/state", "")
	b, _ := io.ReadAll(resp.Body)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("state is served as %q", ct)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("state may be sniffed as something else")
	}
	if strings.Contains(string(b), "<script") || strings.Contains(string(b), "<img") {
		t.Errorf("state carries markup unescaped: %s", b)
	}
	csp := h.ok("GET", "/", "").Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "base-uri 'none'", "form-action 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %s", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe-") {
		t.Errorf("CSP %q allows unsafe sources", csp)
	}
	page, _ := io.ReadAll(h.ok("GET", "/", "").Body)
	if strings.Contains(string(page), "alert(1)") {
		t.Errorf("the page itself carries state: %s", page)
	}
}

// The page's scripts build the DOM from text nodes and attributes only. These are the ways a
// string becomes markup or code; none may appear, so nothing from the bus can.
func TestScriptsHaveNoHTMLSinks(t *testing.T) {
	sinks := []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(",
		"new Function", "srcdoc", "setTimeout(\"", "setTimeout('", "createContextualFragment", "DOMParser"}
	err := fs.WalkDir(static, "static/assets", func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(path, ".js") {
			return err
		}
		b, err := static.ReadFile(path)
		if err != nil {
			return err
		}
		for _, s := range sinks {
			if strings.Contains(string(b), s) {
				t.Errorf("%s uses %s", path, s)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
