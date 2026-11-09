package client

import (
	"net"
	"testing"
	"time"
)

// A session's connection ends for the daemon when the session closes it, even while another copy
// of its descriptor is open, as it is in a child forked at that moment that hasn't yet started its
// program (#217).
func TestSessionCloseEndsTheConnectionDespiteACopy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	defer server.Close()
	// File returns a duplicate of the descriptor: the copy a forked child would hold.
	copied, err := raw.(*net.TCPConn).File()
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()

	sessionConn{raw}.Close()
	server.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := server.Read(make([]byte, 1)); err == nil || isTimeout(err) {
		t.Fatalf("the daemon's side read %v, not the end of the connection", err)
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}
