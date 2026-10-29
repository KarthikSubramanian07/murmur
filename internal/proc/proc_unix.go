//go:build !windows

package proc

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Lock takes an exclusive lock on path, held until the file is closed or the process exits,
// however it exits.
func Lock(path string) (*os.File, error) {
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
	return f, nil
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
