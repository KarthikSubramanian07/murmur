package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/proto"
)

// The first line on the sessions' port is read before its token is checked, so its length is capped.

// The largest request a session may send, a send whose body is all characters JSON escapes, fits
// under the cap, and is read whole.
func TestTheLargestSendFitsTheLineCap(t *testing.T) {
	path := "/" + strings.Repeat("p", 4095)
	req := proto.Request{Token: strings.Repeat("t", 32), Op: proto.OpSend, Session: "aaaaaa",
		To: strings.Repeat("d", 200), Subject: strings.Repeat("<", 200), Body: strings.Repeat("<", maxBody),
		Cwd: path, GitDir: path, Transcript: path}
	b, _ := json.Marshal(req)
	b = append(b, '\n')
	if len(b) > maxRequest {
		t.Fatalf("the largest send is %d bytes, over the cap of %d", len(b), maxRequest)
	}
	line, err := readLine(bufio.NewReader(bytes.NewReader(b)), maxRequest)
	if err != nil || !bytes.Equal(line, b) {
		t.Fatalf("read %d of %d bytes: %v", len(line), len(b), err)
	}
}

// countingConn counts the bytes read from it.
type countingConn struct {
	net.Conn
	n atomic.Int64
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// A connection that sends and sends without a newline is answered and closed once it passes the
// cap, not read until its deadline, and the daemon holds no more of it than the cap.
func TestALineWithoutEndIsCutOff(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	counted := &countingConn{Conn: server}
	go func() {
		chunk := bytes.Repeat([]byte("a"), 64<<10)
		for {
			if _, err := client.Write(chunk); err != nil {
				return
			}
		}
	}()
	d := &Daemon{token: "tok"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.handleConn(context.Background(), counted)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon is still reading the line")
	}
	if n := counted.n.Load(); n > maxRequest+64<<10 {
		t.Errorf("read %d bytes of a line capped at %d", n, maxRequest)
	}
}
