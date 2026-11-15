//go:build !windows

package desktop

import (
	"os"
	"os/exec"
	"syscall"
)

// Detach starts the window's process in a session of its own, so closing the terminal it was
// asked for in doesn't end it.
func Detach(exe string, args []string, out *os.File) error {
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
