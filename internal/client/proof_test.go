package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/proto"
)

// Tests of the daemon's proof (#371): a client that found its daemon goes on using its address, and
// each new connection to it must show that the daemon still answers there, before anything is sent.

// die ends the stand-in as a crash would: its port and lock are let go, and its daemon.json stays.
func (d *standIn) die() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ln.Close()
	d.lock.Close()
	d.lock = nil
}

// connected is a client that found d, and may not start a daemon of its own.
func connected(t *testing.T, gitDir string) *Client {
	t.Helper()
	c := &Client{GitDir: gitDir, Spawn: true, MaySpawn: func() bool { return false }}
	c.launch = func() error {
		t.Error("a daemon was started")
		return nil
	}
	if err := c.connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c
}

// A request on a client that found its daemon, with no hello in between to find it again, doesn't
// go to what took the port after the daemon died, nor take its answer: the session's outgoing
// messages would reach it, and its forged inbox would reach the agent.
func TestARequestDoesNotGoToWhatTookADeadDaemonsPort(t *testing.T) {
	gitDir := stateOnly(t)
	d := startStandIn(t, gitDir, proto.Protocol)
	c := connected(t, gitDir)
	addr := d.ln.Addr().String()
	d.die()
	m := listenImpostor(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if resp, err := c.Do(ctx, proto.Request{Op: proto.OpInbox}); err == nil {
		t.Errorf("the inbox was answered: %+v", resp)
	}
	if got := m.requests(); len(got) > 0 {
		t.Errorf("the listener at the dead daemon's address was sent %v", got)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.tokens) > 0 {
		t.Errorf("the listener at the dead daemon's address was sent the token")
	}
}

// A client that may start a daemon starts one in the dead one's place, and the request goes there.
func TestARequestAfterTheDaemonDiedGoesToANewOne(t *testing.T) {
	gitDir := stateOnly(t)
	d := startStandIn(t, gitDir, proto.Protocol)
	c := &Client{GitDir: gitDir, Spawn: true}
	var fresh *standIn
	c.launch = func() error {
		fresh = startStandIn(t, gitDir, proto.Protocol)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.connect(ctx); err != nil {
		t.Fatal(err)
	}
	addr := d.ln.Addr().String()
	d.die()
	m := listenImpostor(t, addr)
	resp, err := c.Do(ctx, proto.Request{Op: proto.OpInbox})
	if err != nil || !resp.OK {
		t.Fatalf("inbox: %v %+v", err, resp)
	}
	if fresh == nil || !fresh.got(proto.OpInbox) {
		t.Error("the inbox didn't reach a new daemon")
	}
	if got := m.requests(); len(got) > 0 {
		t.Errorf("the listener at the dead daemon's address was sent %v", got)
	}
}

// What took a dead daemon's port can answer with no end of bytes. The client gives up on it after a
// few KB, at once, not when the request's deadline comes, holding all it was sent until then.
func TestAnEndlessAnswerFromWhatTookThePortIsCutOff(t *testing.T) {
	gitDir := stateOnly(t)
	d := startStandIn(t, gitDir, proto.Protocol)
	c := connected(t, gitDir)
	addr := d.ln.Addr().String()
	d.die()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				bufio.NewReader(conn).ReadBytes('\n')
				chunk := []byte(`{"ok":true,"self":"` + strings.Repeat("x", 64<<10))
				for sent := 0; sent < 16<<20; sent += len(chunk) {
					if _, err := conn.Write(chunk); err != nil {
						return
					}
				}
				<-done // and never a newline
			}()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	began := time.Now()
	if _, err := c.Do(ctx, proto.Request{Op: proto.OpInbox}); err == nil {
		t.Error("the endless answer was taken")
	}
	if took := time.Since(began); took > 5*time.Second {
		t.Errorf("the client read the endless answer for %v", took)
	}
}

// The daemon's own reply is bounded too, at a size no real one reaches.
func TestAnOversizedReplyIsRefused(t *testing.T) {
	limit := maxReply
	maxReply = 1 << 20
	t.Cleanup(func() { maxReply = limit })
	gitDir := stateOnly(t)
	d := startStandIn(t, gitDir, proto.Protocol)
	d.mu.Lock()
	d.bigReply = 2 << 20
	d.mu.Unlock()
	c := connected(t, gitDir)
	if resp, err := c.Do(context.Background(), proto.Request{Op: proto.OpInbox}); err == nil {
		t.Errorf("a reply of %d bytes was taken", len(resp.Self))
	}
	d.mu.Lock()
	d.bigReply = 1000
	d.mu.Unlock()
	if resp, err := c.Do(context.Background(), proto.Request{Op: proto.OpInbox}); err != nil || !resp.OK {
		t.Errorf("a reply under the limit: %v %+v", err, resp)
	}
}

// The largest reply a real daemon gives, a full inbox of the longest messages, every character of
// them escaped, fits.
func TestTheReplyLimitFitsAFullInbox(t *testing.T) {
	const maxInbox, maxBody, escaped = 200, 32 << 10, 6
	if need := maxInbox * maxBody * escaped; maxReply < need+need/4 {
		t.Errorf("replies are cut at %d bytes; a full inbox may take %d", maxReply, need)
	}
}

// listenAnswering listens at addr and answers every line with reply, counting the lines that carry
// a token.
func listenAnswering(t *testing.T, addr string, reply func(proto.Request) proto.Response) *atomic.Int32 {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var tokens atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				for {
					line, err := r.ReadBytes('\n')
					if err != nil {
						return
					}
					var req proto.Request
					json.Unmarshal(line, &req)
					if req.Token != "" {
						tokens.Add(1)
					}
					b, _ := json.Marshal(reply(req))
					conn.Write(append(b, '\n'))
				}
			}()
		}
	}()
	return &tokens
}

// What took the port can answer the proof's ping as a daemon from before proof does. That doesn't
// make the client take it for one: such a daemon holds the clone's lock, and a dead one's doesn't.
func TestWhatTookThePortCanNotPassForAnOlderDaemon(t *testing.T) {
	gitDir := stateOnly(t)
	d := startStandIn(t, gitDir, proto.Protocol)
	c := connected(t, gitDir)
	addr := d.ln.Addr().String()
	d.die()
	tokens := listenAnswering(t, addr, func(proto.Request) proto.Response { return proto.Response{Error: "bad token"} })
	if resp, err := c.Do(context.Background(), proto.Request{Op: proto.OpInbox}); err == nil {
		t.Errorf("the inbox was answered: %+v", resp)
	}
	if n := tokens.Load(); n > 0 {
		t.Errorf("the listener at the dead daemon's address was sent the token %d times", n)
	}
}

// A proof is good only for the nonce and the address it was made for: one made for another
// listener, or for another nonce, is refused.
func TestAProofForAnotherAddressOrNonceIsRefused(t *testing.T) {
	gitDir := stateOnly(t)
	d := startStandIn(t, gitDir, proto.Protocol)
	c := connected(t, gitDir)
	info := c.current()
	addr := d.ln.Addr().String()
	d.die()
	for name, proof := range map[string]func(nonce string) string{
		"another address": func(nonce string) string { return proto.Proof(info.Token, "127.0.0.1:1", nonce) },
		"another nonce":   func(string) string { return proto.Proof(info.Token, addr, "replayed") },
	} {
		t.Run(name, func(t *testing.T) {
			tokens := listenAnswering(t, addr, func(req proto.Request) proto.Response {
				return proto.Response{OK: true, Protocol: proto.Protocol, Proof: proof(req.Nonce)}
			})
			if _, err := ping(context.Background(), info); !errors.Is(err, errNotDaemon) {
				t.Errorf("ping: %v, want errNotDaemon", err)
			}
			if _, _, err := c.exchange(context.Background(), info, false, proto.Request{Op: proto.OpInbox}); err == nil {
				t.Error("the inbox was answered")
			}
			if n := tokens.Load(); n > 0 {
				t.Errorf("the listener was sent the token %d times", n)
			}
		})
	}
}

// A daemon from before proof can't show that a connection is to it, so it is sent nothing: no
// request, no hello, and no stop, each of which would carry the token. The client says why, and how
// to end it, by its process ID.
func TestNothingGoesToADaemonFromBeforeProof(t *testing.T) {
	gitDir := stateOnly(t)
	old := startLegacyStandIn(t, gitDir)
	c := connected(t, gitDir)
	if !c.Outdated() {
		t.Error("the client doesn't take its daemon to be older")
	}
	pid := fmt.Sprint(os.Getpid()) // the stand-in's daemon.json names this process
	for op, err := range map[string]error{
		"route": func() error { _, err := c.Do(context.Background(), proto.Request{Op: proto.OpRoute}); return err }(),
		"hello": func() error {
			conn, _, err := c.Hello(context.Background(), proto.Hello{ID: "aaaaaa"})
			if conn != nil {
				conn.Close()
			}
			return err
		}(),
	} {
		if !errors.Is(err, errOlderDaemon) || !strings.Contains(err.Error(), pid) {
			t.Errorf("%s: %v, want errOlderDaemon naming process %s", op, err, pid)
		}
	}
	if stopped, err := Stop(context.Background(), gitDir); !errors.Is(err, errOlderDaemon) {
		t.Errorf("stop: stopped %v, err %v, want errOlderDaemon", stopped, err)
	}
	old.mu.Lock()
	defer old.mu.Unlock()
	if len(old.ops) > 0 {
		t.Errorf("the older daemon was sent %v", old.ops)
	}
}

// A client that may start daemons doesn't replace one from before proof either, as that takes a
// stop: it says how to end it, and starts none beside it.
func TestADaemonFromBeforeProofIsNotReplaced(t *testing.T) {
	gitDir := stateOnly(t)
	old := startLegacyStandIn(t, gitDir)
	c := &Client{GitDir: gitDir, Spawn: true}
	c.launch = func() error {
		t.Error("a daemon was started beside the older one")
		return nil
	}
	if err := c.connect(context.Background()); !errors.Is(err, errOlderDaemon) {
		t.Errorf("connect: %v, want errOlderDaemon", err)
	}
	if old.got(proto.OpStop) {
		t.Error("the older daemon was sent a stop, and its token")
	}
}

// A daemon from before 07a1ecba gave up its port as it began to stop, and kept its lock and its
// daemon.json while it finished, so another process could take the port while both still said the
// daemon was there (Sol's reviews of #389). A stop sent then would give that process the token,
// with which it could prove itself to the next client, beside the old daemon's lock and file. It is
// never sent the token, so the next client doesn't take it for the daemon.
func TestWhatTakesAStoppingOlderDaemonsPortIsNeverSentItsToken(t *testing.T) {
	gitDir := stateOnly(t)
	old := startLegacyStandIn(t, gitDir)
	c := connected(t, gitDir)
	addr := old.ln.Addr().String()
	old.ln.Close() // it keeps its lock and its daemon.json
	// What takes the port answers as an older daemon does, until it is given the token; then it
	// proves itself with it.
	var mu sync.Mutex
	var token string
	tokens := listenAnswering(t, addr, func(req proto.Request) proto.Response {
		mu.Lock()
		defer mu.Unlock()
		if req.Token != "" {
			token = req.Token
		}
		if req.Nonce != "" && token != "" {
			return proto.Response{OK: true, Protocol: proto.Protocol, Proof: proto.Proof(token, addr, req.Nonce)}
		}
		if req.Token == "" {
			return proto.Response{Error: "bad token"}
		}
		return proto.Response{OK: true}
	})
	Stop(context.Background(), gitDir)
	c.Do(context.Background(), proto.Request{Op: proto.OpInbox})
	if conn, _, err := c.Hello(context.Background(), proto.Hello{ID: "aaaaaa"}); err == nil {
		conn.Close()
		t.Error("the hello was taken")
	}
	fresh := &Client{GitDir: gitDir}
	if fresh.found(context.Background()) && !fresh.legacy {
		t.Error("a new client took what took the port for a daemon that proved itself")
	}
	if n := tokens.Load(); n > 0 {
		t.Errorf("what took the port was sent the token %d times", n)
	}
}
