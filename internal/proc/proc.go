// Package proc holds the operating-system specifics of the daemon's process: the lock that
// keeps it to one per clone, and starting it detached from the session that launched it.
package proc

import "errors"

// ErrLocked means another process holds the lock.
var ErrLocked = errors.New("locked by another process")
