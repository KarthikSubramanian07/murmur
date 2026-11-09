//go:build !windows

package join

// shortPath is p: only Windows has short names, and only Windows needs them here.
func shortPath(p string) string { return p }
