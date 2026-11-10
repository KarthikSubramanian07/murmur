package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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

	"github.com/a-Fig/murmur/internal/proc"
	"github.com/a-Fig/murmur/internal/proto"
)

// Tests of replacing a daemon that an older mur left running (#57): an upgrade replaces the binary,
// not the daemon already started from it.

// standIn is a daemon inside the test: it holds the clone's lock, publishes its daemon.json, and
// answers a ping with the protocol number it was given. One with protocol 0 is a daemon from before
// the number, which knows no request that came later and says so. A stop ends it as it ends the real
// one: the file goes, then the lock is let go. A slow one (slowStop) takes its time over that, as the
// real one does while it drains and says goodbye: it takes no more connections, and keeps its file and
// its lock until it is done.
type standIn struct {
	protocol int
	legacy   bool // from before proof: it refuses the proof's ping for its missing token
	token    string
	ln       net.Listener
	gitDir   string

	mu        sync.Mutex
	lock      *proc.Held
	ops       []string
	stopAfter time.Duration
	bigReply  int // if set, an inbox's reply is about this long
}

func startStandIn(t *testing.T, gitDir string, protocol int) *standIn {
	t.Helper()
	return startStandInAs(t, gitDir, protocol, false)
}

// startLegacyStandIn is a stand-in for a daemon from before proof (protocol 11 or older).
func startLegacyStandIn(t *testing.T, gitDir string) *standIn {
	t.Helper()
	return startStandInAs(t, gitDir, proto.ProofProtocol-1, true)
}

func startStandInAs(t *testing.T, gitDir string, protocol int, legacy bool) *standIn {
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
	d := &standIn{protocol: protocol, legacy: legacy, token: fmt.Sprintf("token-%d-%d", protocol, time.Now().UnixNano()), ln: ln, lock: lock, gitDir: gitDir}
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
	r := bufio.NewReader(conn)
	reply := func(r proto.Response) {
		b, _ := json.Marshal(r)
		conn.Write(append(b, '\n'))
	}
	line, err := r.ReadBytes('\n')
	if err != nil {
		return
	}
	var req proto.Request
	if json.Unmarshal(line, &req) != nil {
		return
	}
	// The proof, from a daemon of a protocol that has it, whatever number it reports, as a daemon of
	// 12 will for a client of 13; one from before refuses the ping's missing token, as the real one
	// does.
	if req.Op == proto.OpPing && req.Nonce != "" && !d.legacy {
		reply(proto.Response{OK: true, Protocol: d.protocol, Proof: proto.Proof(d.token, conn.LocalAddr().String(), req.Nonce)})
		if line, err = r.ReadBytes('\n'); err != nil {
			return
		}
		req = proto.Request{}
		if json.Unmarshal(line, &req) != nil {
			return
		}
	}
	if req.Token != d.token {
		reply(proto.Response{Error: "bad token"})
		return
	}
	d.mu.Lock()
	d.ops = append(d.ops, req.Op)
	big := d.bigReply
	d.mu.Unlock()
	if big > 0 && req.Op == proto.OpInbox {
		reply(proto.Response{OK: true, Self: strings.Repeat("x", big)})
		return
	}
	switch req.Op {
	case proto.OpPing:
		reply(proto.Response{OK: true, Protocol: d.protocol})
	case proto.OpStop:
		reply(proto.Response{OK: true})
		d.mu.Lock()
		after := d.stopAfter
		d.mu.Unlock()
		if after > 0 {
			d.ln.Close()
			time.AfterFunc(after, d.exit)
			return
		}
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

// slowStop makes the stand-in take that long, after it is asked to stop, to let go of the lock.
func (d *standIn) slowStop(after time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopAfter = after
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

// A daemon left running by an older mur answers a ping, and not what came after it. A client that may
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
	if !c.found(context.Background()) || !c.Outdated() {
		t.Fatal("the stand-in isn't found as an older daemon")
	}
	old.ln.Close() // it keeps the lock and its file, and answers nothing
	if err := c.replace(context.Background()); err == nil || !strings.Contains(err.Error(), "didn't exit") {
		t.Errorf("replace: %v", err)
	}
}

// A daemon asked to stop takes up to proto.DrainTimeout letting its background work finish and
// proto.GoodbyeTimeout saying goodbye, and holds the clone's lock until it is done (#83). Replacing it
// waits that long, not the 15 s it once did, and then starts the replacement.
func TestAnOlderDaemonThatIsSlowToStopIsWaitedFor(t *testing.T) {
	t.Parallel()
	gitDir := stateOnly(t)
	old := startStandIn(t, gitDir, 0)
	old.slowStop(16 * time.Second) // more than replace once waited, less than a shutdown may take
	var fresh *standIn
	c := &Client{GitDir: gitDir, Spawn: true}
	c.launch = func() error {
		fresh = startStandIn(t, gitDir, proto.Protocol) // fails if the lock is still held
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := c.connect(ctx); err != nil {
		t.Fatal(err)
	}
	if fresh == nil || !old.got(proto.OpStop) {
		t.Errorf("replacement started: %v; older daemon asked to stop: %v", fresh != nil, old.got(proto.OpStop))
	}
}

// The wait is as long as a shutdown may take, and a little more for the request to arrive and the
// process to go.
func TestReplaceWaitCoversAShutdown(t *testing.T) {
	if replaceWait <= proto.ShutdownMax {
		t.Errorf("replace waits %v for a daemon that may take %v to stop", replaceWait, proto.ShutdownMax)
	}
}

// However long a shutdown may take, a caller who gives up is not kept waiting for it.
func TestReplacingAnOlderDaemonEndsWhenTheCallerGivesUp(t *testing.T) {
	gitDir := stateOnly(t)
	old := startStandIn(t, gitDir, 0)
	old.slowStop(time.Hour)
	c := &Client{GitDir: gitDir, Spawn: true}
	c.launch = func() error {
		t.Error("a daemon was started while the older one holds the lock")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	began := time.Now()
	if err := c.connect(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("connect: %v, want the caller's deadline", err)
	}
	if took := time.Since(began); took > 10*time.Second {
		t.Errorf("connect took %v after the caller gave up", took)
	}
}
