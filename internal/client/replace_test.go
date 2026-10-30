package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a-Fig/t4/internal/proc"
	"github.com/a-Fig/t4/internal/proto"
)

// Tests of replacing a daemon that an older t4 left running (#57): an upgrade replaces the binary,
// not the daemon already started from it.

// standIn is a daemon inside the test: it holds the clone's lock, publishes its daemon.json, and
// answers a ping with the protocol number it was given. One with protocol 0 is a daemon from before
// the number, which knows no request that came later and says so. A stop ends it as it ends the real
// one: the file goes, then the lock is let go.
type standIn struct {
	protocol int
	token    string
	ln       net.Listener
	gitDir   string

	mu   sync.Mutex
	lock *proc.Held
	ops  []string
}

func startStandIn(t *testing.T, gitDir string, protocol int) *standIn {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := proc.Lock(filepath.Join(StateDir(gitDir), "daemon.lock"))
	if err != nil {
		ln.Close()
		t.Fatalf("the stand-in daemon can't take the lock: %v", err)
	}
	d := &standIn{protocol: protocol, token: fmt.Sprintf("token-%d-%d", protocol, time.Now().UnixNano()), ln: ln, lock: lock, gitDir: gitDir}
	b, _ := json.Marshal(proto.Info{Addr: ln.Addr().String(), Token: d.token, PID: os.Getpid()})
	if err := os.WriteFile(filepath.Join(StateDir(gitDir), "daemon.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.exit)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go d.serve(conn)
		}
	}()
	return d
}

func (d *standIn) serve(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req proto.Request
	if json.Unmarshal(line, &req) != nil || req.Token != d.token {
		return
	}
	d.mu.Lock()
	d.ops = append(d.ops, req.Op)
	d.mu.Unlock()
	reply := func(r proto.Response) {
		b, _ := json.Marshal(r)
		conn.Write(append(b, '\n'))
	}
	switch req.Op {
	case proto.OpPing:
		reply(proto.Response{OK: true, Protocol: d.protocol})
	case proto.OpStop:
		reply(proto.Response{OK: true})
		go d.exit()
	case proto.OpHello:
		reply(proto.Response{OK: true, Self: "dev/x"})
		io.Copy(io.Discard, conn) // a session stays registered while its connection is open
	default:
		if d.protocol == 0 {
			reply(proto.Response{Error: "unknown op " + req.Op})
			return
		}
		reply(proto.Response{OK: true})
	}
}

func (d *standIn) exit() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lock == nil {
		return
	}
	d.ln.Close()
	os.Remove(filepath.Join(StateDir(d.gitDir), "daemon.json"))
	d.lock.Close()
	d.lock = nil
}

func (d *standIn) got(op string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Contains(d.ops, op)
}

func stateOnly(t *testing.T) string {
	t.Helper()
	gitDir, _ := lockPath(t)
	return gitDir
}

// A daemon left running by an older t4 answers a ping, and not what came after it. A client that may
// start daemons stops it and starts its own, and its next request reaches that one.
func TestAnOlderDaemonIsStoppedAndReplaced(t *testing.T) {
	gitDir := stateOnly(t)
	old := startStandIn(t, gitDir, 0)
	var fresh *standIn
	c := &Client{GitDir: gitDir, Spawn: true}
	c.launch = func() error {
		fresh = startStandIn(t, gitDir, proto.Protocol)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.connect(ctx); err != nil {
		t.Fatal(err)
	}
	if fresh == nil {
		t.Fatal("no new daemon was started")
	}
	if !old.got(proto.OpStop) {
		t.Error("the older daemon wasn't asked to stop")
	}
	resp, err := c.Do(ctx, proto.Request{Op: proto.OpRoute})
	if err != nil || !resp.OK {
		t.Fatalf("route: %v %+v", err, resp)
	}
	if old.got(proto.OpRoute) || !fresh.got(proto.OpRoute) {
		t.Errorf("the request went to the older daemon (%v), not the new one (%v)", old.got(proto.OpRoute), fresh.got(proto.OpRoute))
	}
	if c.Outdated() {
		t.Error("the client still takes its daemon to be older")
	}
}

// A session registers with the replacement, not with the daemon that was running: Hello is where an
// MCP server first meets the daemon.
func TestHelloReplacesAnOlderDaemonFirst(t *testing.T) {
	gitDir := stateOnly(t)
	old := startStandIn(t, gitDir, 0)
	var fresh *standIn
	c := &Client{GitDir: gitDir, Spawn: true}
	c.launch = func() error {
		fresh = startStandIn(t, gitDir, proto.Protocol)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, _, err := c.Hello(ctx, proto.Hello{ID: "aaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if fresh == nil || old.got(proto.OpHello) || !fresh.got(proto.OpHello) {
		t.Errorf("the hello went to the older daemon (%v), not a new one (%v)", old.got(proto.OpHello), fresh != nil && fresh.got(proto.OpHello))
	}
}

// Some clients must take a daemon as it is: `stop` is how an older daemon is ended by hand, and a
// client that may not start daemons (a user who left) must not end one either.
func TestAnOlderDaemonIsLeftToClientsThatMayNotStartOne(t *testing.T) {
	for name, c := range map[string]*Client{
		"no spawn": {Spawn: false},
		"may not":  {Spawn: true, MaySpawn: func() bool { return false }},
	} {
		t.Run(name, func(t *testing.T) {
			gitDir := stateOnly(t)
			old := startStandIn(t, gitDir, 0)
			c.GitDir = gitDir
			c.launch = func() error {
				t.Error("a daemon was started")
				return nil
			}
			if err := c.connect(context.Background()); err != nil {
				t.Fatal(err)
			}
			if old.got(proto.OpStop) {
				t.Error("the older daemon was stopped")
			}
			if !c.Outdated() {
				t.Error("the client doesn't know its daemon is older")
			}
		})
	}
	t.Run("stop", func(t *testing.T) {
		gitDir := stateOnly(t)
		old := startStandIn(t, gitDir, 0)
		stopped, err := Stop(context.Background(), gitDir)
		if err != nil || !stopped || !old.got(proto.OpStop) {
			t.Errorf("stopped=%v err=%v asked to stop=%v", stopped, err, old.got(proto.OpStop))
		}
	})
}

// A daemon of a newer protocol than the client's is not the client's to replace: two binaries of
// different ages on one machine would otherwise stop each other's daemons for ever.
func TestANewerDaemonIsLeftAlone(t *testing.T) {
	gitDir := stateOnly(t)
	newer := startStandIn(t, gitDir, proto.Protocol+1)
	c := &Client{GitDir: gitDir, Spawn: true}
	c.launch = func() error {
		t.Error("a daemon was started")
		return nil
	}
	if err := c.connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if newer.got(proto.OpStop) || c.Outdated() {
		t.Errorf("stopped=%v outdated=%v", newer.got(proto.OpStop), c.Outdated())
	}
}

// An older daemon that won't go leaves the client with an error that says so, not a second daemon
// that would find the lock taken.
func TestAnOlderDaemonThatStaysIsAnError(t *testing.T) {
	wait := replaceWait
	replaceWait = 300 * time.Millisecond
	t.Cleanup(func() { replaceWait = wait })
	gitDir := stateOnly(t)
	old := startStandIn(t, gitDir, 0)
	c := &Client{GitDir: gitDir, Spawn: true}
	c.launch = func() error {
		t.Error("a daemon was started while the older one holds the lock")
		return nil
	}
	if !c.load() || c.ping(context.Background()) != nil || !c.Outdated() {
		t.Fatal("the stand-in isn't found as an older daemon")
	}
	old.ln.Close() // it keeps the lock and its file, and answers nothing
	if err := c.replace(context.Background()); err == nil || !strings.Contains(err.Error(), "didn't exit") {
		t.Errorf("replace: %v", err)
	}
}
