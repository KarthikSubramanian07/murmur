//go:build !windows && !linux && !darwin

package proc

// parentOf finds nothing on systems without a way to look written here.
func parentOf(int) (int, bool) { return 0, false }
