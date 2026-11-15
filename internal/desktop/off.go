//go:build linux && !desktop

package desktop

import (
	"context"
	"errors"
)

// Supported says whether this build has the window. Linux builds leave it out unless built with
// -tags desktop: the web view is loaded through purego, which on Linux makes the Go runtime link
// glibc dynamically, and every mur command would then fail where glibc isn't at the usual path
// (Alpine, NixOS, a distroless container). On Windows and macOS it changes nothing of the kind.
const Supported = false

// Run reports that this build has no window.
func Run(context.Context, Options) error {
	return errors.New("this build of mur has no desktop app (Linux builds leave it out unless built with -tags desktop); `mur ui` opens the same page in a browser")
}
