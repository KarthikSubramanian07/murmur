package join

import "golang.org/x/sys/windows"

// shortPath is p's 8.3 form, which has no spaces, or p where the volume keeps no short names.
func shortPath(p string) string {
	from, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	buf := make([]uint16, 1024)
	n, err := windows.GetShortPathName(from, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return p
	}
	return windows.UTF16ToString(buf[:n])
}
