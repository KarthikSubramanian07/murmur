package proc

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// parentOf is pid's parent, from a snapshot of the system's processes.
func parentOf(pid int) (int, bool) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, false
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if int(e.ProcessID) == pid {
			return int(e.ParentProcessID), true
		}
	}
	return 0, false
}
