package daemon

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// unpoll detaches the page's inherited socket from the I/O completion port it is tied to, so that
// this process's runtime can take it up. A socket is tied to one port for good, and the daemon
// before served the page from this one (#255). Nothing is waiting on the socket by then: that
// daemon stopped serving before it handed the socket on.
func unpoll(f *os.File) error {
	var info struct {
		Port windows.Handle
		Key  uintptr
	}
	var iosb windows.IO_STATUS_BLOCK
	return windows.NtSetInformationFile(windows.Handle(f.Fd()), &iosb, (*byte)(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)), windows.FileReplaceCompletionInformation)
}
