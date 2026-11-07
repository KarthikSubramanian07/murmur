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
