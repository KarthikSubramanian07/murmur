//go:build !windows

package proc

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Lock takes an exclusive lock on path, held until it is closed or the process exits, however it
// exits.
func Lock(path string) (*Held, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &Held{f}, nil
}

// unlock releases the flock itself instead of leaving it to the descriptor's close. A flock belongs
// to the open file description, not to the descriptor, so closing the descriptor releases it only
// if no other descriptor refers to the same description. A process that forks (to run git, say)
// gives its child a copy of every descriptor, and the copy lives until the child's exec closes it,
// which can be a while on a busy machine. A lock closed in that window stays held, and a daemon
// started a moment later is refused with ErrLocked: the daemon tests restart one device while the
// other devices' daemons, in the same process, keep running git (the macOS runner, 2026-10-30).
// LOCK_UN on any one descriptor releases the lock for all of them.
func unlock(f *os.File) {
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// StartDetached starts a program in its own session, so it outlives the process that started
// it and has no terminal to prompt on.
func StartDetached(path string, args []string, dir string, out *os.File) (*os.Process, error) {
	cmd := exec.Command(path, args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}
