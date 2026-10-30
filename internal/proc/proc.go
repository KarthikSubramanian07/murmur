// Package proc holds the operating-system specifics of the daemon's process: the lock that
// keeps it to one per clone, and starting it detached from the session that launched it.
package proc

import (
	"errors"
	"os"
)

// ErrLocked means another process holds the lock.
var ErrLocked = errors.New("locked by another process")

// Held is an exclusive lock on a file, as Lock returns it.
type Held struct{ f *os.File }

// Close releases the lock, and at once: a lock that outlived its Close would make the next Lock
// fail with ErrLocked for a daemon that has already gone. The lock is also released if the
// process exits, however it exits.
func (l *Held) Close() error {
	unlock(l.f)
	return l.f.Close()
}
