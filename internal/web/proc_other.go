//go:build !windows

package web

import "os/exec"

func hideWindow(*exec.Cmd) {}
