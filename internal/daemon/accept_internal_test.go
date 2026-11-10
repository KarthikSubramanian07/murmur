package daemon

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// flakyListener fails its first accepts as a process out of file descriptors does, then hands out
// its connections, then is closed.
type flakyListener struct {
	fails int
	conns chan net.Conn
}

func (l *flakyListener) Accept() (net.Conn, error) {
	if l.fails > 0 {
		l.fails--
		return nil, &net.OpError{Op: "accept", Net: "tcp", Err: os.NewSyscallError("accept", syscall.EMFILE)}
	}
	c, ok := <-l.conns
	if !ok {
		return nil, fmt.Errorf("accept tcp: %w", net.ErrClosed)
	}
	return c, nil
}

func (l *flakyListener) Close() error   { return nil }
func (l *flakyListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

// Running out of file descriptors passes, as connections close: the daemon waits and accepts again
// rather than exiting, which any local user could once make it do. A closed listener still ends it.
func TestTheSessionsPortOutlastsAcceptErrors(t *testing.T) {
	ln := &flakyListener{fails: 4, conns: make(chan net.Conn)}
	d := &Daemon{token: "tok", quit: make(chan struct{}), log: log.New(io.Discard, "", 0)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan struct{})
	go func() {
		defer close(served)
		d.serve(ctx, ln)
	}()

	client, server := net.Pipe()
	defer client.Close()
	select {
	case ln.conns <- server:
	case <-d.quit:
		t.Fatal("the daemon stopped on an accept error")
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon stopped accepting")
	}
	client.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(client, `{"token":"wrong","op":"ping"}`+"\n")
	if reply, err := bufio.NewReader(client).ReadString('\n'); err != nil || !strings.Contains(reply, "bad token") {
		t.Errorf("reply %q, %v", reply, err)
	}

	close(ln.conns)
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon goes on accepting from a closed listener")
	}
	select {
	case <-d.quit:
	default:
		t.Error("a closed listener didn't stop the daemon")
	}
}
