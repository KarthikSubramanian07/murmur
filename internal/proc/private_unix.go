//go:build !windows

package proc

// Protect does nothing here: the 0o700 a directory is made with already keeps other users out.
func Protect(dir string) error { return nil }
