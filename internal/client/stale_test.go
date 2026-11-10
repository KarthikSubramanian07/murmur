package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/proc"
	"github.com/a-Fig/murmur/internal/proto"
)

// Tests of a daemon.json its daemon left behind: one that died without removing it (a crash, SIGKILL,
// a power cut) leaves an address that another user's process can listen on.

// impostor listens at an address and answers every request as a daemon of this protocol would. It
// counts the requests other than pings.
type impostor struct {
	ln net.Listener

	mu     sync.Mutex
	other  []string
	tokens []string      // the tokens it was sent
	pings  chan struct{} // a value for each ping, while there is room
}

func listenImpostor(t *testing.T, addr string) *impostor {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	m := &impostor{ln: ln, pings: make(chan struct{}, 16)}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var req proto.Request
				json.Unmarshal(line, &req)
				if req.Token != "" {
					m.mu.Lock()
					m.tokens = append(m.tokens, req.Token)
					m.mu.Unlock()
				}
				if req.Op == proto.OpPing {
					select {
					case m.pings <- struct{}{}:
					default:
					}
				} else {
					m.mu.Lock()
					m.other = append(m.other, req.Op)
					m.mu.Unlock()
				}
				b, _ := json.Marshal(proto.Response{OK: true, Protocol: proto.Protocol, Self: "dev/x"})
				conn.Write(append(b, '\n'))
				if req.Op == proto.OpHello {
					time.Sleep(time.Second)
				}
			}()
		}
	}()
	return m
}

func (m *impostor) requests() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.other...)
}

// leaveStale writes the daemon.json a dead daemon would leave, naming addr, with no lock held.
func leaveStale(t *testing.T, gitDir, addr string) {
	t.Helper()
	b, _ := json.Marshal(proto.Info{Addr: addr, Token: "the-dead-daemons", PID: 1})
	if err := os.WriteFile(filepath.Join(StateDir(gitDir), "daemon.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// What answers at a stale daemon.json's address isn't the daemon: a client that may not start one
// finds none, and the stale file goes.
func TestAStaleDaemonJSONIsNoDaemon(t *testing.T) {
	gitDir := stateOnly(t)
	m := listenImpostor(t, "127.0.0.1:0")
	leaveStale(t, gitDir, m.ln.Addr().String())
	c, err := Connect(context.Background(), gitDir, false)
	if !errors.Is(err, ErrNoDaemon) {
		t.Fatalf("connect: %v, want ErrNoDaemon", err)
	}
	if _, err := c.Do(context.Background(), proto.Request{Op: proto.OpNotices}); err == nil {
		t.Error("a request was answered")
	}
	if got := m.requests(); len(got) > 0 {
		t.Errorf("the listener at the stale address was sent %v", got)
	}
	if _, err := os.Stat(filepath.Join(StateDir(gitDir), "daemon.json")); err == nil {
		t.Error("the stale daemon.json is still there")
	}
}

// A client that may start a daemon starts one, and takes it, not what answers at the old address
// while the new one is still coming up. The new daemon holds the lock from its start and writes its
// daemon.json only later, as the real one does after its first sync.
func TestAStaleDaemonJSONIsReplacedByANewDaemon(t *testing.T) {
	gitDir := stateOnly(t)
	m := listenImpostor(t, "127.0.0.1:0")
	leaveStale(t, gitDir, m.ln.Addr().String())
	var fresh *standIn
	ready := make(chan struct{})
	c := &Client{GitDir: gitDir, Spawn: true}
	c.launch = func() error {
		go func() {
			defer close(ready)
			time.Sleep(500 * time.Millisecond)
			fresh = startStandIn(t, gitDir, proto.Protocol)
		}()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, _, err := c.Hello(ctx, proto.Hello{ID: "aaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Error("no daemon was started")
	}
	if got := m.requests(); len(got) > 0 {
		t.Errorf("the listener at the stale address was sent %v", got)
	}
	if fresh == nil || !fresh.got(proto.OpHello) {
		t.Error("the hello didn't reach the new daemon")
	}
}

// A client that had a daemon forgets it once it is gone: an MCP server reconnects when its session's
// connection drops, and its requests must not go on to whatever listens at the dead daemon's address.
func TestAClientForgetsADaemonThatDied(t *testing.T) {
	gitDir := stateOnly(t)
	d := startStandIn(t, gitDir, proto.Protocol)
	c := &Client{GitDir: gitDir, Spawn: true, MaySpawn: func() bool { return false }}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.connect(ctx); err != nil {
		t.Fatal(err)
	}
	addr := d.ln.Addr().String()
	// It dies without removing its daemon.json, and something else takes its port.
	d.mu.Lock()
	d.ln.Close()
	d.lock.Close()
	d.lock = nil
	d.mu.Unlock()
	m := listenImpostor(t, addr)
	if _, _, err := c.Hello(ctx, proto.Hello{ID: "aaaaaa"}); err == nil {
		t.Error("a hello was taken")
	}
	if _, err := c.Do(ctx, proto.Request{Op: proto.OpNotices}); err == nil {
		t.Error("a request was answered")
	}
	if got := m.requests(); len(got) > 0 {
		t.Errorf("the listener at the dead daemon's address was sent %v", got)
	}
}

// Another client holding the lock, here while it removes the stale file, doesn't make a stale
// daemon.json look live, however long it holds it: clients take the lock shared, and only a
// daemon's exclusive lock refuses a client's.
func TestAnotherClientsLockDoesNotMakeAStaleFileLive(t *testing.T) {
	gitDir := stateOnly(t)
	m := listenImpostor(t, "127.0.0.1:0")
	leaveStale(t, gitDir, m.ln.Addr().String())
	held, err := proc.LockShared(filepath.Join(StateDir(gitDir), "daemon.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if _, err := Connect(context.Background(), gitDir, false); !errors.Is(err, ErrNoDaemon) {
		t.Fatalf("connect: %v, want ErrNoDaemon", err)
	}
	if got := m.requests(); len(got) > 0 {
		t.Errorf("the listener at the dead daemon's address was sent %v", got)
	}
}
