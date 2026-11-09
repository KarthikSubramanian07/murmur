package join

import (
	"context"
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

func setLeaveWait(t *testing.T, wait time.Duration) {
	was := leaveWait
	leaveWait = wait
	t.Cleanup(func() { leaveWait = was })
}

// A daemon asked to stop may hold the lock for proto.ShutdownMax; a leave waits at least that long
// before it calls the daemon a problem, not the 20 s it once did (#91).
func TestLeaveWaitCoversAShutdown(t *testing.T) {
	if leaveWait <= proto.ShutdownMax {
		t.Errorf("a leave waits %v for a daemon that may take %v to stop", leaveWait, proto.ShutdownMax)
	}
}

// A daemon that takes its time to let go of the lock is waited for and reported as stopped.
func TestLeaveWaitsForADaemonThatIsSlowToStop(t *testing.T) {
	sb := newSandbox(t, false)
	p, _ := clone(t)
	release := holdLock(t, p)
	time.AfterFunc(500*time.Millisecond, release)
	r, err := Leave(context.Background(), sb.Env, p.Top)
	if err != nil || len(r.Problems) != 0 {
		t.Fatalf("err %v, problems %q", err, r.Problems)
	}
	if !slices.Contains(r.Did, "Stopped this clone's daemon") {
		t.Errorf("did %q", r.Did)
	}
}

// A daemon still holding the lock when the wait is over is a problem, not a stopped daemon.
func TestLeaveReportsADaemonThatStays(t *testing.T) {
	setLeaveWait(t, 300*time.Millisecond)
	sb := newSandbox(t, false)
	p, _ := clone(t)
	holdLock(t, p)
	r, err := Leave(context.Background(), sb.Env, p.Top)
	if err == nil || len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "waiting for the daemon to exit: "+client.ErrStillRunning.Error()) {
		t.Fatalf("err %v, problems %q", err, r.Problems)
	}
	if slices.Contains(r.Did, "Stopped this clone's daemon") {
		t.Errorf("did %q", r.Did)
	}
}

// However long a shutdown may take, a caller who gives up is not kept waiting for it.
func TestLeaveEndsWhenTheCallerGivesUp(t *testing.T) {
	sb := newSandbox(t, false)
	p, _ := clone(t)
	holdLock(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	r, err := Leave(ctx, sb.Env, p.Top)
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("the leave took %v after its caller gave up", took)
	}
	if err == nil || len(r.Problems) != 1 || !strings.Contains(r.Problems[0], context.DeadlineExceeded.Error()) {
		t.Errorf("err %v, problems %q", err, r.Problems)
	}
}
