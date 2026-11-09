//go:build !windows

package daemon

import "os"

// unpoll is for Windows, where a socket stays tied to the completion port of the process that
// first served it. A descriptor here takes no such tie with it.
func unpoll(*os.File) error { return nil }
