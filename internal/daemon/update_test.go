package daemon

import (
	"context"
	"testing"
	"time"
)

func TestUpdateWaitsForDelivery(t *testing.T) {
	d := &Daemon{pushing: map[string]bool{"session": true}, quit: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { d.restartWhenIdle(ctx); close(done) }()
	select {
	case <-d.quit:
		t.Fatal("stopped during delivery")
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
		t.Fatal("new deliveries not blocked")
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
		t.Fatal("blocked deliveries while the page is open")
	}
	// The last visible-page heartbeat ages out, but a delivery now in flight
	// must still finish before restarting.
	d.lastPage = time.Now().Add(-2 * d.opt.PageOpen)
	d.pushing["session"] = true
	d.mu.Unlock()
	select {
	case <-d.quit:
		t.Fatal("stopped during delivery after page expiry")
	case <-time.After(150 * time.Millisecond):
	}
	d.mu.Lock()
	delete(d.pushing, "session")
	d.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("did not restart after page expired and delivery finished")
	}
	select {
	case <-d.quit:
	default:
		t.Fatal("not stopped")
	}
}
