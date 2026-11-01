//go:build windows

package attrib_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/a-Fig/murmur/internal/attrib"
)

type caseProc struct {
	pid, ppid uint32
	exe       string
}

func caseProcesses() (map[uint32]caseProc, error) {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer syscall.CloseHandle(snap)
	var e syscall.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	m := map[uint32]caseProc{}
	for err = syscall.Process32First(snap, &e); err == nil; err = syscall.Process32Next(snap, &e) {
		m[e.ProcessID] = caseProc{e.ProcessID, e.ParentProcessID, syscall.UTF16ToString(e.ExeFile[:])}
	}
	if !errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return m, nil
}

// caseWatch records every process descended from this test binary while it runs, including
// grandchildren whose parent has already exited, and closes started when the first appears.
type caseWatch struct {
	mu      sync.Mutex
	seen    map[uint32]caseProc
	started chan struct{}
	stop    chan struct{}
	done    chan struct{}
}

func caseWatchChildren(t *testing.T) *caseWatch {
	w := &caseWatch{seen: map[uint32]caseProc{}, started: make(chan struct{}), stop: make(chan struct{}), done: make(chan struct{})}
	self := uint32(os.Getpid())
	go func() {
		defer close(w.done)
		signalled := false
		for {
			select {
			case <-w.stop:
				return
			default:
			}
			procs, err := caseProcesses()
			if err != nil {
				t.Errorf("process snapshot: %v", err)
				return
			}
			w.mu.Lock()
			for grew := true; grew; {
				grew = false
				for pid, p := range procs {
					if _, ok := w.seen[pid]; ok {
						continue
					}
					if _, parent := w.seen[p.ppid]; p.ppid == self || parent {
						w.seen[pid] = p
						grew = true
					}
				}
			}
			n := len(w.seen)
			w.mu.Unlock()
			if n > 0 && !signalled {
				signalled = true
				close(w.started)
			}
			time.Sleep(time.Millisecond)
		}
	}()
	return w
}

// leaked stops watching and returns the descendants still alive after grace.
func (w *caseWatch) leaked(t *testing.T, grace time.Duration) []caseProc {
	close(w.stop)
	<-w.done
	deadline := time.Now().Add(grace)
	for {
		procs, err := caseProcesses()
		if err != nil {
			t.Fatal(err)
		}
		var alive []caseProc
		w.mu.Lock()
		for pid, p := range w.seen {
			if q, ok := procs[pid]; ok && q.exe == p.exe && q.ppid == p.ppid {
				alive = append(alive, p)
			}
		}
		w.mu.Unlock()
		if len(alive) == 0 || time.Now().After(deadline) {
			return alive
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A context cancelled while git runs makes the call return an error promptly, and leaves no git
// process behind.
func TestCancelMidCall(t *testing.T) {
	t.Run("Head", func(t *testing.T) {
		d := caseRepo(t)
		caseCommit(t, d, "base", caseHourAgo())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		w := caseWatchChildren(t)
		go func() { <-w.started; cancel() }()
		_, err := attrib.Head(ctx, d)
		if err == nil {
			t.Error("Head returned no error after its context was cancelled")
		}
		if alive := w.leaked(t, 3*time.Second); len(alive) > 0 {
			t.Errorf("processes still running: %+v", alive)
		}
	})
	t.Run("NewCommits", func(t *testing.T) {
		d := caseRepo(t)
		before := caseHead(t, d)
		// A long history no ref reached at before, as when a command fetches and checks out a
		// large unrelated project, so the walk takes long enough to cancel in the middle.
		caseFastImport(t, d, 150000, nil, false)
		after := caseHead(t, d)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		w := caseWatchChildren(t)
		cancelledAt := make(chan time.Time, 1)
		go func() {
			<-w.started
			time.Sleep(300 * time.Millisecond)
			cancelledAt <- time.Now()
			cancel()
		}()
		start := time.Now()
		_, err := attrib.NewCommits(ctx, d, before, after)
		took := time.Since(start)
		if err == nil {
			t.Errorf("NewCommits returned no error after its context was cancelled (took %v)", took)
		}
		select {
		case at := <-cancelledAt:
			if late := time.Since(at); late > 6*time.Second {
				t.Errorf("NewCommits returned %v after cancel", late)
			}
		default:
			t.Errorf("NewCommits finished in %v, before it could be cancelled", took)
		}
		if alive := w.leaked(t, 3*time.Second); len(alive) > 0 {
			t.Errorf("processes still running: %+v", alive)
		}
	})
}
