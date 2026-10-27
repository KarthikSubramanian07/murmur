package proc

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Lock takes an exclusive lock on path, held until the file is closed or the process exits,
// however it exits.
func Lock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	var ol windows.Overlapped
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
	if err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return f, nil
}

const (
	createNoWindow         = 0x08000000
	createBreakawayFromJob = 0x01000000
)

// StartDetached starts a program with a hidden console, in its own process group, and outside
// the starter's job object where allowed. Agent clients built on libuv put their children in a
// job that is killed when the client exits; breaking away keeps the daemon alive for the
// client's other sessions. If the job forbids breakaway, the daemon starts inside it anyway, and
// the next session to need a daemon starts another.
func StartDetached(path string, args []string, dir string, out *os.File) (*os.Process, error) {
	start := func(flags uint32) (*os.Process, error) {
		cmd := exec.Command(path, args...)
		cmd.Dir, cmd.Stdout, cmd.Stderr = dir, out, out
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cmd.Process, nil
	}
	flags := uint32(createNoWindow | syscall.CREATE_NEW_PROCESS_GROUP)
	p, err := start(flags | createBreakawayFromJob)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		p, err = start(flags)
	}
	return p, err
}
