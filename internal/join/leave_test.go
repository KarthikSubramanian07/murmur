package join

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/client"
	"github.com/a-Fig/murmur/internal/proc"
	"github.com/a-Fig/murmur/internal/proto"
)

// holdLock takes the clone's daemon lock, as a daemon that is stopping holds it, and returns what
// lets go of it, which the test's end does too.
func holdLock(t *testing.T, p Place) (release func()) {
	t.Helper()
	dir := client.StateDir(p.GitDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := proc.Lock(filepath.Join(dir, "daemon.lock"))
	if err != nil {
		t.Fatal(err)
	}
	release = sync.OnceFunc(func() { l.Close() })
	t.Cleanup(release)
	return release
}

func setLeaveTimes(t *testing.T, wait, quiet time.Duration) {
	wasWait, wasQuiet := leaveWait, leaveQuiet
	leaveWait, leaveQuiet = wait, quiet
	t.Cleanup(func() { leaveWait, leaveQuiet = wasWait, wasQuiet })
}

// A daemon asked to stop may hold the lock for proto.ShutdownMax; a leave waits at least that long
// before it calls the daemon a problem, not the 20 s it once did (#91).
func TestLeaveWaitCoversAShutdown(t *testing.T) {
	if leaveWait <= proto.ShutdownMax {
		t.Errorf("a leave waits %v for a daemon that may take %v to stop", leaveWait, proto.ShutdownMax)
	}
}

// onWait has the leave call f where it waits for the lock.
func onWait(t *testing.T, f func(ctx context.Context, gitDir string, d time.Duration) (bool, error)) {
	was := waitGone
	waitGone = f
	t.Cleanup(func() { waitGone = was })
}

// signal is a writer that says when it is first written to.
type signal struct {
	once    sync.Once
	written chan struct{}
	mu      sync.Mutex
	b       strings.Builder
}

func newSignal() *signal { return &signal{written: make(chan struct{})} }

func (s *signal) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.b.Write(p)
	s.mu.Unlock()
	s.once.Do(func() { close(s.written) })
	return len(p), nil
}

func (s *signal) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// A daemon that takes its time to let go of the lock is waited for and reported as stopped, and the
// leave says what it is waiting for while it does. The lock is let go of only once the leave is
// waiting and has said so, however long the git commands before the wait took.
func TestLeaveWaitsForADaemonThatIsSlowToStop(t *testing.T) {
	setLeaveTimes(t, client.StopWait, 50*time.Millisecond)
	sb := newSandbox(t, false)
	said := newSignal()
	sb.Progress = said
	p, _ := clone(t)
	release := holdLock(t, p)
	onWait(t, func(ctx context.Context, gitDir string, d time.Duration) (bool, error) {
		// The lock is still held as the wait begins.
		held, err := client.WaitGone(ctx, gitDir, 0)
		if !held || !errors.Is(err, client.ErrStillRunning) {
			t.Errorf("as the wait began: held %v, err %v", held, err)
		}
		select {
		case <-said.written:
		case <-time.After(time.Minute):
			t.Error("the leave never said it was waiting")
		}
		time.AfterFunc(100*time.Millisecond, release) // while the real wait below is looking
		gone, err := client.WaitGone(ctx, gitDir, d)
		return held || gone, err
	})
	r, err := Leave(context.Background(), sb.Env, p.Top)
	if err != nil || len(r.Problems) != 0 {
		t.Fatalf("err %v, problems %q", err, r.Problems)
	}
	if !slices.Contains(r.Did, "Stopped this clone's daemon") {
		t.Errorf("did %q", r.Did)
	}
	if !strings.HasPrefix(said.String(), "Waiting for this clone's daemon to stop") {
		t.Errorf("while waiting, said %q", said.String())
	}
}

// A daemon still holding the lock when the wait is over is a problem, not a stopped daemon.
func TestLeaveReportsADaemonThatStays(t *testing.T) {
	setLeaveTimes(t, 300*time.Millisecond, time.Hour)
	sb := newSandbox(t, false)
	var said bytes.Buffer
	sb.Progress = &said
	p, _ := clone(t)
	holdLock(t, p)
	r, err := Leave(context.Background(), sb.Env, p.Top)
	if err == nil || len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "waiting for the daemon to exit: "+client.ErrStillRunning.Error()) {
		t.Fatalf("err %v, problems %q", err, r.Problems)
	}
	if slices.Contains(r.Did, "Stopped this clone's daemon") {
		t.Errorf("did %q", r.Did)
	}
	if said.Len() != 0 {
		t.Errorf("said %q before it had waited long", said.String())
	}
}

// However long a shutdown may take, a caller who gives up is not kept waiting for it.
func TestLeaveEndsWhenTheCallerGivesUp(t *testing.T) {
	sb := newSandbox(t, false)
	p, _ := clone(t)
	holdLock(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The caller gives up once the leave is waiting for the lock, not before: the git commands
	// before the wait would fail on a context that ended early, which isn't what this is about.
	var gaveUp time.Time
	onWait(t, func(ctx context.Context, gitDir string, d time.Duration) (bool, error) {
		gaveUp = time.Now()
		cancel()
		return client.WaitGone(ctx, gitDir, d)
	})
	r, err := Leave(ctx, sb.Env, p.Top)
	if gaveUp.IsZero() {
		t.Fatal("the leave never waited for the lock")
	}
	if took := time.Since(gaveUp); took > 10*time.Second {
		t.Errorf("the leave took %v after its caller gave up", took)
	}
	if err == nil || len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "waiting for the daemon to exit: "+context.Canceled.Error()) {
		t.Errorf("err %v, problems %q", err, r.Problems)
	}
}
