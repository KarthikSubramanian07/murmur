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
// On giving up, abandon is called, and must end cmd's context. A start that comes back later is
// then stopped by exec as for any context that has ended (cmd.Cancel, then a kill after
// cmd.WaitDelay), and waited for here, so it doesn't stay a zombie. But it may run before that
// reaches it: the command can do what it was started to do, late, after the caller has moved on.
// Only a command whose late run the caller can live with belongs here.
func StartWithin(cmd *exec.Cmd, d time.Duration, abandon func()) error {
	t := time.NewTimer(d)
	defer t.Stop()
	if err := startWithin(cmd, t.C, abandon, (*exec.Cmd).Start, nil); err != nil {
		if errors.Is(err, ErrStartHung) {
			return fmt.Errorf("%w (%s)", err, d)
		}
		return err
	}
	return nil
}

// startWithin is StartWithin, giving up when giveUp fires, with start in place of cmd.Start and
// reaped, if set, told how a start that came back late ended.
func startWithin(cmd *exec.Cmd, giveUp <-chan time.Time, abandon func(), start func(*exec.Cmd) error, reaped func(error)) error {
	done := make(chan error, 1)
	go func() { done <- start(cmd) }()
	select {
	case err := <-done:
		return err
	case <-giveUp:
	}
	abandon()
	go func() {
		err := <-done
		if err == nil {
			err = cmd.Wait()
		}
		if reaped != nil {
			reaped(err)
		}
	}()
	return ErrStartHung
}
