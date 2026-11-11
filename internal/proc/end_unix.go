//go:build linux || darwin

package proc

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

func end(pid int, is func(exe string) bool) error {
	// Before the look at what it runs: on Linux this takes a pidfd, so the signal below goes to the
	// process looked at, or to none.
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer p.Release()
	exe, err := exeOf(pid)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			return ErrGone
		}
		return err
	}
	if !is(exe) {
		return ErrOther
	}
	err = p.Signal(syscall.SIGTERM)
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return ErrGone
	}
	return err
}
