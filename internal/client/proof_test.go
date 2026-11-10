package client

import (
	"bufio"
	"context"
	"net"
	"strings"
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

// A daemon from before proof can't show it is still the daemon, except by holding the clone's lock.
// A client that may not replace it uses it while it does, and not after it dies and something else
// takes its port.
func TestADaemonFromBeforeProofIsTrustedOnlyWhileItHoldsTheLock(t *testing.T) {
	gitDir := stateOnly(t)
	old := startStandIn(t, gitDir, proto.ProofProtocol-1)
	c := connected(t, gitDir)
	if !c.Outdated() {
		t.Error("the client doesn't take its daemon to be older")
	}
	if resp, err := c.Do(context.Background(), proto.Request{Op: proto.OpRoute}); err != nil || !resp.OK {
		t.Fatalf("route: %v %+v", err, resp)
	}
	addr := old.ln.Addr().String()
	old.die()
	m := listenImpostor(t, addr)
	if _, err := c.Do(context.Background(), proto.Request{Op: proto.OpRoute}); err == nil {
		t.Error("a request was answered after the daemon died")
	}
	if got := m.requests(); len(got) > 0 {
		t.Errorf("the listener at the dead daemon's address was sent %v", got)
	}
}
