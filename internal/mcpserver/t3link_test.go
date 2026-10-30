package mcpserver

import (
	"context"
	"testing"
	"time"
)

// More tests of how the server finds its T3 thread (#57), across the server's turning on and off.

// useFinder makes the server's lookups in T3's database ask fd.
func useFinder(t *testing.T, fd *finder) {
	t.Helper()
	old := threadOf
	threadOf = func(ctx context.Context, _, _, native string) (string, error) { return fd.find(ctx, native) }
	t.Cleanup(func() { threadOf = old })
}

// A link made when the server turns on again, after a leave and a join, finds its thread as the
// first did: the finder is the link's, set up where the link is made.
func TestLinkMadeOnReactivationStillFindsItsThread(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "claude-session-1")
	t.Setenv("T3CODE_CLI_PATH", "/t3/bin/t3")
	fd := &finder{threads: map[string]string{"claude-session-1": "thread-1"}}
	useFinder(t, fd)
	f := &fakeDaemon{}
	p := repo(t, true, f)
	if _, err := p.RecordConsent(); err != nil {
		t.Fatal(err)
	}
	cs, _ := open(t, p.Top, 25*time.Millisecond)
	waitFor(t, "the first registration", func() bool { return f.helloCount() == 1 })
	if h := f.lastHello(); h.T3Thread != "thread-1" {
		t.Fatalf("the first hello: %+v", h.Route)
	}

	if _, err := p.ForgetConsent(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the tools to go", func() bool { return len(toolNamesOf(t, cs)) == 0 })
	if _, err := p.RecordConsent(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the second registration", func() bool { return f.helloCount() == 2 })
	if h := f.lastHello(); h.T3Thread != "thread-1" {
		t.Errorf("the hello after joining again: %+v", h.Route)
	}
	if n := len(fd.askedAbout()); n != 2 {
		t.Errorf("T3's database was asked %d times, want once for each link", n)
	}
}
