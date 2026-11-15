package desktop

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Detach starts the window's process on its own, with no console: mur is a console program, and
// the window outlives the terminal it was asked for in. A terminal in a job that forbids breaking
// away (an agent's) keeps it in the job, which ends it with the terminal.
func Detach(exe string, args []string, out *os.File) error {
	const flags = windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP
	err := start(exe, args, out, flags|windows.CREATE_BREAKAWAY_FROM_JOB)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		err = start(exe, args, out, flags)
	}
	return err
}

func start(exe string, args []string, out *os.File, flags uint32) error {
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
