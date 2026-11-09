package daemon

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/web"
)

func TestUpdateWaitsForHandoffs(t *testing.T) {
	d := &Daemon{pushing: map[string]bool{"session": true}, quit: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { d.restartWhenIdle(ctx); close(done) }()
	select {
	case <-d.quit:
		t.Fatal("stopped during handoff")
	case <-time.After(30 * time.Millisecond):
	}
	d.mu.Lock()
	delete(d.pushing, "session")
	d.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("did not restart")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.updating {
		t.Fatal("new handoffs not blocked")
	}
	select {
	case <-d.quit:
	default:
		t.Fatal("not stopped")
	}
}

func TestUpdateWaitsForOpenPage(t *testing.T) {
	d := &Daemon{opt: Options{PageOpen: time.Hour}, lastPage: time.Now(),
		pushing: map[string]bool{}, quit: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { d.restartWhenIdle(ctx); close(done) }()
	select {
	case <-d.quit:
		t.Fatal("stranded the open page")
	case <-time.After(30 * time.Millisecond):
	}
	d.mu.Lock()
	if d.updating {
		d.mu.Unlock()
		t.Fatal("blocked handoffs while the page is open")
	}
	// The last visible-page heartbeat ages out, but a handoff now in flight
	// must still finish before restarting.
	d.lastPage = time.Now().Add(-2 * d.opt.PageOpen)
	d.pushing["session"] = true
	d.mu.Unlock()
	select {
	case <-d.quit:
		t.Fatal("stopped during handoff after page expiry")
	case <-time.After(150 * time.Millisecond):
	}
	d.mu.Lock()
	delete(d.pushing, "session")
	d.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("did not restart after page expired and handoff finished")
	}
	select {
	case <-d.quit:
	default:
		t.Fatal("not stopped")
	}
}

// An open page holds an installed update back, until the page asks for the restart (#240).
func TestPageAsksForRestart(t *testing.T) {
	d := &Daemon{opt: Options{PageOpen: time.Hour}, lastPage: time.Now(), started: time.Now(),
		pushing: map[string]bool{}, quit: make(chan struct{})}
	src := pageSource{d}
	if err := src.Restart(context.Background()); !errors.Is(err, web.ErrNoUpdate) {
		t.Fatalf("restart with nothing installed: %v, want ErrNoUpdate", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { d.restartWhenIdle(ctx); close(done) }()
	for {
		st, err := src.State(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if st.Version.Ready {
			if st.Version.Daemon == "" {
				t.Error("the page can't tell this daemon from the next")
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("the page never heard the update is ready")
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case <-d.quit:
		t.Fatal("stranded the open page")
	case <-time.After(150 * time.Millisecond):
	}
	if err := src.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("did not restart when the page asked")
	}
	select {
	case <-d.quit:
	default:
		t.Fatal("not stopped")
	}
}

// A daemon restarting onto an update leaves the page's address and token to the next one, which
// serves the page there, so the open tab reconnects (#240).
func TestPageHandoff(t *testing.T) {
	dir := t.TempDir()
	if _, ok := takePageHandoff(dir); ok {
		t.Fatal("handoff from nowhere")
	}
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := free.Addr().String()
	free.Close()
	if err := writeJSON(filepath.Join(dir, pageHandoffFile), pageHandoff{Web: addr, Token: "tok"}); err != nil {
		t.Fatal(err)
	}
	h, ok := takePageHandoff(dir)
	if !ok || h.Web != addr || h.Token != "tok" {
		t.Fatalf("handoff %+v %v", h, ok)
	}
	if _, ok := takePageHandoff(dir); ok {
		t.Fatal("a handoff was taken twice")
	}

	// A stale one is dropped.
	path := filepath.Join(dir, pageHandoffFile)
	writeJSON(path, pageHandoff{Web: addr, Token: "tok"})
	old := time.Now().Add(-2 * pageHandoffAge)
	os.Chtimes(path, old, old)
	if _, ok := takePageHandoff(dir); ok {
		t.Fatal("took a stale handoff")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale handoff left behind: %v", err)
	}

	d := &Daemon{opt: Options{PageAddr: defaultPageAddr}, pagePrev: addr, log: log.New(io.Discard, "", 0)}
	ln, err := d.listenPage()
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if ln.Addr().String() != addr {
		t.Fatalf("page on %s, want the old address %s", ln.Addr(), addr)
	}
	// With the old address taken, the page goes elsewhere rather than not at all, and with a new
	// token: whatever took the port has been sent the old one.
	d.pageTok = "tok"
	ln2, err := d.listenPage()
	if err != nil {
		t.Fatal(err)
	}
	ln2.Close()
	if d.pageTok == "tok" || d.pageTok == "" {
		t.Fatalf("the page moved and kept token %q", d.pageTok)
	}
}
