package proc

import (
	"os"
	"strconv"
)

// exeOf is the path of the program pid runs: where /proc/<pid>/exe points. Linux adds " (deleted)"
// to the path of a file that has been removed or replaced since.
func exeOf(pid int) (string, error) {
	return os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
}
