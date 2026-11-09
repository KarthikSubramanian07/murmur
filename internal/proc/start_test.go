package proc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	began := time.Now()
	err := startWithin(cmd, 100*time.Millisecond, func() { close(abandoned) }, hung)
	if !errors.Is(err, ErrStartHung) {
		t.Fatalf("got %v, want ErrStartHung", err)
	}
	if took := time.Since(began); took > 5*time.Second {
		t.Fatalf("gave up after %s, not soon after 100ms", took)
	}
	select {
	case <-abandoned:
	default:
		t.Fatal("abandon wasn't called")
	}
}

// A start that comes back late, after its context has ended, is killed: the command doesn't go on
// running behind the caller's back.
func TestStartWithinKillsALateStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The test binary itself, which then (see init) waits a second and writes a file: a command
	// left running would leave the file behind.
	ran := filepath.Join(t.TempDir(), "ran")
	cmd := exec.CommandContext(ctx, exe, "-test.run=^$")
	cmd.Env = append(os.Environ(), lateEnv+"="+ran)
	// The process starts at once, and Start comes back only after StartWithin has given up, as a
	// fork whose exec came late would.
	release := make(chan struct{})
	started := make(chan error, 1)
	late := func(c *exec.Cmd) error {
		err := c.Start()
		started <- err
		<-release
		return err
	}
	if err := startWithin(cmd, 50*time.Millisecond, cancel, late); !errors.Is(err, ErrStartHung) {
		t.Fatalf("got %v, want ErrStartHung", err)
	}
	close(release)
	if err := <-started; err != nil {
		t.Fatalf("starting the command: %v", err)
	}
	time.Sleep(4 * time.Second)
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("the command that started late went on running")
	}
}

// lateEnv makes the test binary a command that writes a file a second after it starts.
const lateEnv = "MURMUR_PROC_TEST_LATE"

func init() {
	if path := os.Getenv(lateEnv); path != "" {
		time.Sleep(time.Second)
		os.WriteFile(path, []byte("ran"), 0o600)
		os.Exit(0)
	}
}
