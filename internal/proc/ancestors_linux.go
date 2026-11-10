package proc

import (
	"os"
	"strconv"
	"strings"
)

// parentOf is pid's parent, the fourth field of /proc/<pid>/stat. The second field, the command's
// name in parentheses, may hold spaces and parentheses itself, so the fields are counted from the
// last closing parenthesis.
func parentOf(pid int) (int, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(f[1])
	return ppid, err == nil
}
