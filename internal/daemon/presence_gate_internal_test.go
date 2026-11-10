package daemon

import (
	"context"
	"testing"
	"time"
)

// A writer that comes to the gate after a presence has taken it, while that presence is still being
// encoded or waiting for the push slot, waits for it as one that came earlier does. The gate was let
// go the moment the presence was made, so such a writer pushed ahead of it.
func TestPresenceGateHoldsWhileSending(t *testing.T) {
	d := &Daemon{}
	gate := make(chan struct{})
	d.presenceFirst = gate

	// What publishPresence does as it makes the presence, under d.mu.
	d.mu.Lock()
	d.presenceFirst, d.presenceSending = nil, gate
	d.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := d.awaitPresence(ctx); err == nil {
		t.Fatal("a writer went ahead of the presence being sent")
	}

	// A session starts meanwhile; its writer waits for the presence after this one too.
	next := make(chan struct{})
	d.mu.Lock()
	d.presenceFirst = next
	d.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- d.awaitPresence(context.Background()) }()

	d.presenceSent(gate)
	select {
	case err := <-done:
		t.Fatalf("the writer went before the next presence: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if d.presenceSending != nil {
		t.Error("presenceSending is left set after its presence was sent")
	}
	close(next)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the writer is still waiting after both presences")
	}
	if err := d.awaitPresence(context.Background()); err != nil {
		t.Fatal(err)
	}
}
