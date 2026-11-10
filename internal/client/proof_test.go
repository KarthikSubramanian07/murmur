package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
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

// A daemon from before proof can't show that a connection is to it, so a client that may not
// replace one sends it nothing but a stop, and says why.
func TestOnlyAStopGoesToADaemonFromBeforeProof(t *testing.T) {
	gitDir := stateOnly(t)
	old := startStandIn(t, gitDir, proto.ProofProtocol-1)
	c := connected(t, gitDir)
	if !c.Outdated() {
		t.Error("the client doesn't take its daemon to be older")
	}
	if _, err := c.Do(context.Background(), proto.Request{Op: proto.OpRoute}); !errors.Is(err, errOlderDaemon) {
		t.Errorf("route: %v, want errOlderDaemon", err)
	}
	if conn, _, err := c.Hello(context.Background(), proto.Hello{ID: "aaaaaa"}); !errors.Is(err, errOlderDaemon) {
		if conn != nil {
			conn.Close()
		}
		t.Errorf("hello: %v, want errOlderDaemon", err)
	}
	if old.got(proto.OpRoute) || old.got(proto.OpHello) {
		t.Error("the older daemon was sent a request")
	}
	if resp, err := c.Do(context.Background(), proto.Request{Op: proto.OpStop}); err != nil || !resp.OK || !old.got(proto.OpStop) {
		t.Errorf("stop: %v %+v, asked: %v", err, resp, old.got(proto.OpStop))
	}
}

// A daemon from before 07a1ecba gave up its port as it began to stop, and kept its lock and its
// daemon.json while it finished, so another process could take the port while both still said the
// daemon was there (Sol's review of #389). The session's requests don't go to it.
func TestADaemonFromBeforeProofThatIsStoppingIsSentNothingButAStop(t *testing.T) {
	gitDir := stateOnly(t)
	old := startStandIn(t, gitDir, proto.ProofProtocol-1)
	c := connected(t, gitDir)
	addr := old.ln.Addr().String()
	old.ln.Close() // it keeps its lock and its daemon.json
	m := listenImpostor(t, addr)
	if _, err := c.Do(context.Background(), proto.Request{Op: proto.OpInbox}); err == nil {
		t.Error("the inbox was answered")
	}
	if conn, _, err := c.Hello(context.Background(), proto.Hello{ID: "aaaaaa"}); err == nil {
		conn.Close()
		t.Error("the hello was taken")
	}
	if got := m.requests(); len(got) > 0 {
		t.Errorf("what took the port was sent %v", got)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.tokens) > 0 {
		t.Error("what took the port was sent the token")
	}
}

// dieOnDial makes old die, and something else take its port, as the client is about to make its
// nth connection from now: after any check made before the dial, and before the connection is
// made. It returns what took the port, once it has.
func dieOnDial(t *testing.T, old *standIn, n int) func() *impostor {
	t.Helper()
	addr := old.ln.Addr().String()
	var m *impostor
	dials := 0
	beforeDial = func(to string) {
		if to != addr {
			return
		}
		if dials++; dials == n {
			old.die()
			m = listenImpostor(t, addr)
		}
	}
	t.Cleanup(func() { beforeDial = nil })
	return func() *impostor { return m }
}

// A stop for a daemon from before proof checks the clone's lock once its connection is made, not
// before the dial: the daemon can die between the two, and whatever takes its port then isn't sent
// the token (Sol's review of main before v0.3.0-rc.1).
func TestAStopForADaemonFromBeforeProofThatDiesAsTheClientDialsIsNotSent(t *testing.T) {
	gitDir := stateOnly(t)
	old := startStandIn(t, gitDir, proto.ProofProtocol-1)
	c := connected(t, gitDir)
	took := dieOnDial(t, old, 1)
	if _, sent, err := c.roundTrip(context.Background(), proto.Request{Op: proto.OpStop}); sent || err == nil {
		t.Errorf("the stop: sent %v, err %v", sent, err)
	}
	m := took()
	if m == nil {
		t.Fatal("the daemon didn't die as the client dialed")
	}
	if got := m.requests(); len(got) > 0 {
		t.Errorf("what took the port was sent %v", got)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.tokens) > 0 {
		t.Error("what took the port was sent the token")
	}
}
