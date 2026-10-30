package t3

import (
	"os/exec"
	"syscall"
)

// createNoWindow keeps the command from opening a console window when its parent, the detached
// daemon, has none.
const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
