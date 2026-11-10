package proc

import (
	"errors"
	"path/filepath"
	"testing"
)

// Clients take the clone's lock shared and a daemon takes it exclusively, so a client can tell a
// daemon's lock from another client's: shared locks sit together, and each kind refuses the other.
func TestSharedAndExclusiveLocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock")
	a, err := LockShared(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LockShared(path)
	if err != nil {
		t.Fatalf("a second shared lock: %v", err)
	}
	if l, err := Lock(path); !errors.Is(err, ErrLocked) {
		if l != nil {
			l.Close()
		}
		t.Fatalf("the exclusive lock beside shared ones: %v, want ErrLocked", err)
	}
	a.Close()
	b.Close()
	ex, err := Lock(path)
	if err != nil {
		t.Fatalf("the exclusive lock once the shared ones are gone: %v", err)
	}
	defer ex.Close()
	if l, err := LockShared(path); !errors.Is(err, ErrLocked) {
		if l != nil {
			l.Close()
		}
		t.Fatalf("a shared lock beside the exclusive one: %v, want ErrLocked", err)
	}
}
