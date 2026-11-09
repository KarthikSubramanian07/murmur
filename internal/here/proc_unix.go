//go:build !windows

package here

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// parentsOf maps every running process to its parent, from ps, which Linux and macOS both have.
func parentsOf() (map[int]int, error) {
	b, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=").Output()
	if err != nil {
		return nil, err
	}
	out := map[int]int{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 == nil && err2 == nil {
			out[pid] = ppid
		}
	}
	return out, nil
}

// alive reports whether the process is still running.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Started is when the process started, as ps prints it, only compared for equality; "" if unknown.
func Started(pid int) string {
	b, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
