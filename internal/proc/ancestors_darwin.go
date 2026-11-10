package proc

import "golang.org/x/sys/unix"

// parentOf is pid's parent, from the kernel's process table.
func parentOf(pid int) (int, bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, false
	}
	return int(kp.Eproc.Ppid), true
}
