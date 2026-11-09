package proc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

// A start that never comes back is given up on, and the caller gets an error in time, not a wait
// for good (#217).
func TestStartWithinGivesUpOnAStartThatHangs(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	hung := func(*exec.Cmd) error { <-release; return errors.New("released") }
	abandoned := make(chan struct{})
	cmd := exec.Command("git", "--version")
	giveUp := make(chan time.Time)
	result := make(chan error, 1)
	go func() { result <- startWithin(cmd, giveUp, func() { close(abandoned) }, hung, nil) }()
	close(giveUp)
	select {
	case err := <-result:
		if !errors.Is(err, ErrStartHung) {
			t.Fatalf("got %v, want ErrStartHung", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("still waiting on a start that hangs")
	}
	select {
	case <-abandoned:
	default:
		t.Fatal("abandon wasn't called")
	}
}

// A command whose Start comes back after StartWithin gave up is stopped once its context ends, and
// reaped. The test can't hold the real Start inside its fork, so it covers what comes after: a
// process that exists by the time its context ends, whose Start hadn't come back to the caller.
func TestStartWithinStopsAStartThatCameBackLate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The test binary itself, which then (see init) sleeps for a minute: one that exits sooner was
	// stopped.
	cmd := exec.CommandContext(ctx, exe, "-test.run=^$")
	cmd.Env = append(os.Environ(), sleepEnv+"=1")
	cmd.WaitDelay = time.Second
	started := make(chan error, 1)
	release := make(chan struct{})
	late := func(c *exec.Cmd) error {
		err := c.Start()
		started <- err
		<-release
		return err
	}
	giveUp := make(chan time.Time)
	reaped := make(chan error, 1)
	result := make(chan error, 1)
	go func() {
		result <- startWithin(cmd, giveUp, cancel, late, func(err error) { reaped <- err })
	}()
	if err := <-started; err != nil {
		t.Fatalf("starting the command: %v", err)
	}
	close(giveUp)
	if err := <-result; !errors.Is(err, ErrStartHung) {
		t.Fatalf("got %v, want ErrStartHung", err)
	}
	close(release)
	began := time.Now()
	select {
	case err := <-reaped:
		if err == nil {
			t.Fatal("the command exited cleanly, so it wasn't stopped")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the command that came back late wasn't stopped and reaped")
	}
	if took := time.Since(began); took > 20*time.Second {
		t.Fatalf("stopping the late command took %s", took)
	}
}

// sleepEnv makes the test binary a command that sleeps for a minute.
const sleepEnv = "MURMUR_PROC_TEST_SLEEP"

func init() {
	if os.Getenv(sleepEnv) != "" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}
