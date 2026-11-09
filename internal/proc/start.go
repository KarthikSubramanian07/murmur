package proc

import (
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// ErrStartHung is what StartWithin's error wraps when the command's start didn't come back in
// time.
var ErrStartHung = errors.New("the command didn't start in time")

// StartWithin starts cmd, and gives up if Start hasn't returned within d.
//
// Start can hang. On GitHub's macOS runners, in the daemon package's tests, a forked child was seen
// never to reach exec (#217), and Start waits for that with nothing to cancel it: the daemon loop
// that ran the command waited for good. Nobody has seen it outside those tests, and 2.8 million
// starts of git on the same runners, with and without -race, never took over a second, but a
// daemon runs for days, and a loop stopped for good is the worst way for it to fail.
//
// On giving up, abandon is called, and must end cmd's context: a start that comes back later is
// then killed at once (exec does that for a context that has ended), and waited for here, so it
// doesn't stay a zombie. Whether a command that started late did anything before it was killed
// can't be told, so the caller mustn't take the error to mean it never ran.
func StartWithin(cmd *exec.Cmd, d time.Duration, abandon func()) error {
	return startWithin(cmd, d, abandon, (*exec.Cmd).Start)
}

func startWithin(cmd *exec.Cmd, d time.Duration, abandon func(), start func(*exec.Cmd) error) error {
	done := make(chan error, 1)
	go func() { done <- start(cmd) }()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case err := <-done:
		return err
	case <-t.C:
	}
	abandon()
	go func() {
		if <-done == nil {
			cmd.Wait()
		}
	}()
	return fmt.Errorf("%w (%s)", ErrStartHung, d)
}
