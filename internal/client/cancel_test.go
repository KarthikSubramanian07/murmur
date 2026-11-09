package client

import (
	"bufio"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/proto"
)

// A request whose context is cancelled while it waits on the daemon's answer returns at once, not
// when the connection's deadline (two minutes) runs out: the MCP server cancels a call when its
// session moves to another repo, and the call mustn't hang on the old repo's daemon.
func TestCancellingEndsARequestWaitingOnTheDaemon(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan struct{}, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		bufio.NewReader(conn).ReadBytes('\n') // and never answer
		got <- struct{}{}
		time.Sleep(time.Minute)
	}()
	c := &Client{info: proto.Info{Addr: ln.Addr().String(), Token: "tok"}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Do(ctx, proto.Request{Op: proto.OpWho})
		done <- err
	}()
	<-got
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Do = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Do still waiting on the daemon 5 s after its context was cancelled")
	}
}

// A request cancelled before it is written is never sent.
func TestACancelledRequestIsNotSent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	c := &Client{info: proto.Info{Addr: ln.Addr().String(), Token: "tok"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, sent, err := c.roundTrip(ctx, proto.Request{Op: proto.OpWho})
	if sent || err == nil {
		t.Errorf("roundTrip with a cancelled context: sent %v, err %v", sent, err)
	}
}
