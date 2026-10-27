//go:build !windows

package gitx

import (
	"os"
	"os/exec"
)

func hideWindow(*exec.Cmd) {}

var interrupt = func(p *os.Process) error { return p.Signal(os.Interrupt) }
