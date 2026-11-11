package proc

import (
	"bytes"
	"io/fs"

	"golang.org/x/sys/unix"
)

// exeOf is the path of the program pid runs, from the kernel's copy of the process's arguments:
// a count of them in the first four bytes, then the path the program was started from, ended by a
// zero byte. The kernel gives it only for the user's own processes.
func exeOf(pid int) (string, error) {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", err
	}
	if len(b) <= 4 {
		return "", fs.ErrNotExist // what a process that is exiting leaves
	}
	b = b[4:]
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b), nil
}
