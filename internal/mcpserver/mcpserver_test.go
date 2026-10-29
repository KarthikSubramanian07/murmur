package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/a-Fig/t4/internal/client"
	"github.com/a-Fig/t4/internal/proto"
)

// fakeDaemon answers hellos and requests the way the daemon does, as far as the server's side of
// following Claude Code's session ID goes: every reply to a request carries the Claude Code
// session ID the daemon holds, and a hello's reply carries the one it was given.
type fakeDaemon struct {
	live string // what the daemon holds, as a hook has set it

	mu     sync.Mutex
	hellos []proto.Hello
	conns  []net.Conn // the hello connections, to drop
}

func (f *fakeDaemon) serve(t *testing.T) (gitDir string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	gitDir = t.TempDir()
	dir := client.StateDir(gitDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	info, _ := json.Marshal(proto.Info{Addr: ln.Addr().String(), Token: "tok"})
	if err := os.WriteFile(filepath.Join(dir, "daemon.json"), info, 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(conn)
		}
	}()
	return gitDir
}

func (f *fakeDaemon) handle(conn net.Conn) {
	r := bufio.NewReader(conn)
	line, err := r.ReadBytes('\n')
	if err != nil {
		conn.Close()
		return
	}
	var req proto.Request
	if json.Unmarshal(line, &req) != nil {
		conn.Close()
		return
	}
	reply := func(resp proto.Response) {
		b, _ := json.Marshal(resp)
		conn.Write(append(b, '\n'))
	}
	if req.Op == proto.OpHello {
		f.mu.Lock()
		f.hellos = append(f.hellos, *req.Hello)
		f.conns = append(f.conns, conn)
		live := f.live
		if live == "" {
			live = req.Hello.ClaudeSession
		}
		f.mu.Unlock()
		reply(proto.Response{OK: true, Self: "dev/" + req.Hello.ID, ClaudeSession: live})
		return // the connection stays open until the test drops it
	}
	defer conn.Close()
	f.mu.Lock()
	live := f.live
	f.mu.Unlock()
	reply(proto.Response{OK: true, Self: "dev/" + req.Session, ClaudeSession: live})
}

func (f *fakeDaemon) helloCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hellos)
}

func (f *fakeDaemon) lastHello() proto.Hello {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hellos[len(f.hellos)-1]
}

func (f *fakeDaemon) hook(claude string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live = claude
}

func (f *fakeDaemon) dropConnections() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		c.Close()
	}
	f.conns = nil
}

// The hello carries the server's parent process, which is the Claude Code process that started it,
// and the Claude Code session ID in its environment.
func TestHelloNamesTheParentProcess(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "0a1b2c3d-aaaa-bbbb-cccc-ddddeeeeffff")
	h := helloFor("claude-code", "/work")
	if h.ClientPID != os.Getppid() || h.ClientPID == 0 {
		t.Fatalf("the hello says the parent is %d, want %d", h.ClientPID, os.Getppid())
	}
	if h.ClaudeSession != "0a1b2c3d-aaaa-bbbb-cccc-ddddeeeeffff" || h.ID != "0a1b2c" || h.Cwd != "/work" {
		t.Fatalf("hello: %+v", h)
	}
}

// A reconnect (the daemon restarted, say) must not bring back the Claude Code session ID the
// server's environment has, which a /clear made stale: after any call, the server registers with
// the one the daemon reported.
func TestReconnectBringsTheLiveClaudeID(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "0a1b2c3d-aaaa-bbbb-cccc-ddddeeeeffff")
	f := &fakeDaemon{}
	gitDir := f.serve(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &client.Client{GitDir: gitDir}
	s := &server{c: c, link: &link{c: c, hello: helloFor("claude-code", "/work"), changed: make(chan struct{})}}
	go s.link.run(ctx)

	if _, err := s.call(ctx, proto.Request{Op: proto.OpWho}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if h := f.lastHello(); h.ClaudeSession != "0a1b2c3d-aaaa-bbbb-cccc-ddddeeeeffff" {
		t.Fatalf("first hello: %+v", h)
	}

	// /clear: a hook told the daemon. The server learns it with its next call.
	f.hook("fffffff0-live")
	if _, err := s.call(ctx, proto.Request{Op: proto.OpWho}); err != nil {
		t.Fatalf("call after the switch: %v", err)
	}

	// The daemon's connection drops and the server registers again.
	before := f.helloCount()
	f.dropConnections()
	deadline := time.Now().Add(10 * time.Second)
	for f.helloCount() == before {
		if time.Now().After(deadline) {
			t.Fatal("the server never registered again")
		}
		time.Sleep(20 * time.Millisecond)
	}
	h := f.lastHello()
	if h.ClaudeSession != "fffffff0-live" {
		t.Fatalf("the second hello brought back %q, want the live ID", h.ClaudeSession)
	}
	if h.ID != "0a1b2c" || !h.Reconnect {
		t.Fatalf("the session's own ID must not change: %+v", h)
	}
}
