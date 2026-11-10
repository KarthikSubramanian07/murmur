package daemon_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/client"
	"github.com/a-Fig/murmur/internal/proto"
)

// A daemon that stops gives up its port seconds before it lets go of the lock: it finishes its work
// and says goodbye first. Its daemon.json must be gone by the time the port is free, or another
// user's process could take the port and be trusted as the daemon, which still holds the lock, by
// the sessions that reconnect as their connections drop.
func TestAStoppingDaemonsPortIsNotTrusted(t *testing.T) {
	_, gitDir, joined := consentClone(t)
	joined.Store(true)
	cancel, done := runWithConsent(gitDir, joined, time.Hour)
	defer cancel()
	var c *client.Client
	eventually(t, "the daemon to come up", func() bool {
		var err error
		c, err = client.Connect(context.Background(), gitDir, false)
		return err == nil
	})
	infoPath := filepath.Join(gitDir, "murmur", "daemon.json")
	b, err := os.ReadFile(infoPath)
	if err != nil {
		t.Fatal(err)
	}
	var info proto.Info
	if err := json.Unmarshal(b, &info); err != nil {
		t.Fatal(err)
	}
	// A connection that sends nothing holds the daemon's drain for its 10 s read deadline, so the
	// lock stays held well after the port is given up.
	idle, err := net.Dial("tcp", info.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()
	if resp, err := c.Do(context.Background(), proto.Request{Op: proto.OpStop}); err != nil || resp.Error != "" {
		t.Fatalf("stop: %v %q", err, resp.Error)
	}
	eventually(t, "the port to be given up", func() bool {
		conn, err := net.DialTimeout("tcp", info.Addr, time.Second)
		if err != nil {
			return true
		}
		conn.Close()
		return false
	})
	ln, err := net.Listen("tcp", info.Addr)
	if err != nil {
		t.Fatalf("binding the given-up port: %v", err)
	}
	defer ln.Close()
	var mu sync.Mutex
	var got []string
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var req proto.Request
				json.Unmarshal(line, &req)
				mu.Lock()
				got = append(got, req.Op)
				mu.Unlock()
				b, _ := json.Marshal(proto.Response{OK: true, Protocol: proto.Protocol, Self: "dev/x"})
				conn.Write(append(b, '\n'))
			}()
		}
	}()
	if _, err := client.Connect(context.Background(), gitDir, false); !errors.Is(err, client.ErrNoDaemon) {
		t.Errorf("connect while the daemon stops: %v, want ErrNoDaemon", err)
	}
	mu.Lock()
	if len(got) > 0 {
		t.Errorf("the listener on the given-up port was sent %v", got)
	}
	mu.Unlock()
	idle.Close()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the daemon didn't stop")
	}
}
