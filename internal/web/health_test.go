package web

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func (f *fakeSource) Health(ctx context.Context) (Health, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Health{At: time.Now(), Sync: SyncHealth{LastOKAge: -1, Failures: 3,
		LastError: &HealthError{Class: ErrDNS, Message: "fatal: Could not resolve host: github.com"}}}, nil
}

// The health snapshot is behind the page's token, as everything under /api/ is, and answers with
// an empty list of refs, not null, so the page needn't tell the two apart.
func TestHealthNeedsTheToken(t *testing.T) {
	h := start(t, newFake(), noPRs)
	host := "127.0.0.1:" + h.port
	for _, token := range []string{"", "wrong", testToken + "x"} {
		resp := h.do("GET", "/api/health", host, token, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET /api/health with token %q: %d, want 401", token, resp.StatusCode)
		}
		if b, _ := io.ReadAll(resp.Body); strings.Contains(string(b), "resolve host") {
			t.Errorf("GET /api/health with token %q answered with the snapshot: %s", token, b)
		}
	}
	resp := h.ok("GET", "/api/health", "")
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control %q, want no-store", got)
	}
	noCORS(t, resp)
	b, _ := io.ReadAll(resp.Body)
	for _, want := range []string{`"class":"dns"`, `"consecutiveFailures":3`, `"lastOKAgeSeconds":-1`, `"refs":[]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the snapshot has no %s:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), `"lastOK"`) {
		t.Errorf("a sync that never worked has a time:\n%s", b)
	}
}
