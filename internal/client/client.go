// Package client finds a clone's daemon, starting it if asked, and talks to it.
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/a-Fig/t4/internal/brand"
	"github.com/a-Fig/t4/internal/gitx"
	"github.com/a-Fig/t4/internal/proc"
	"github.com/a-Fig/t4/internal/proto"
)

// ErrNoDaemon means no daemon is running for the clone and the caller didn't ask for one.
var ErrNoDaemon = errors.New("no daemon is running for this clone")

// FindGitDir returns the git common dir of the repository containing dir: shared by all of a
// clone's worktrees, so they share one daemon.
func FindGitDir(ctx context.Context, dir string) (string, error) {
	out, err := gitx.RunIn(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("not inside a git repository (%s)", dir)
	}
	return filepath.Clean(strings.TrimSpace(out)), nil
}

// StateDir is the daemon's directory for a git common dir.
func StateDir(gitDir string) string { return filepath.Join(gitDir, brand.StateDir) }

// Client talks to one clone's daemon. It is safe for concurrent use.
type Client struct {
	GitDir string
	Spawn  bool // start a daemon when none answers

	mu   sync.Mutex
	info proto.Info
}

func (c *Client) current() proto.Info {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

// Connect finds the daemon for gitDir, starting one if spawn is set and none answers.
func Connect(ctx context.Context, gitDir string, spawn bool) (*Client, error) {
	c := &Client{GitDir: gitDir, Spawn: spawn}
	return c, c.connect(ctx)
}

func (c *Client) connect(ctx context.Context) error {
	if c.load() && c.ping(ctx) == nil {
		return nil
	}
	if !c.Spawn {
		return ErrNoDaemon
	}
	if err := c.start(); err != nil {
		return fmt.Errorf("starting the daemon: %w", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
		if c.load() && c.ping(ctx) == nil {
			return nil
		}
	}
	return fmt.Errorf("the daemon didn't come up; see %s", filepath.Join(StateDir(c.GitDir), "daemon.log"))
}

func (c *Client) load() bool {
	b, err := os.ReadFile(filepath.Join(StateDir(c.GitDir), "daemon.json"))
	if err != nil {
		return false
	}
	var info proto.Info
	if json.Unmarshal(b, &info) != nil || info.Addr == "" {
		return false
	}
	c.mu.Lock()
	c.info = info
	c.mu.Unlock()
	return true
}

func (c *Client) ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	resp, _, err := c.roundTrip(ctx, proto.Request{Op: proto.OpPing})
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	return nil
}

// start launches a daemon detached from this process. If two clients start one at once, the
// second daemon finds the lock taken and exits.
func (c *Client) start() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := StateDir(c.GitDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	logPath := filepath.Join(dir, "daemon.log")
	if fi, err := os.Stat(logPath); err == nil && fi.Size() > 4<<20 {
		os.Rename(logPath, logPath+".old")
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	// Run in the clone's top directory, where git resolves a relative remote URL from. The
	// daemon still never uses it as a working tree: every command it runs gets --git-dir.
	cwd := c.GitDir
	if filepath.Base(cwd) == ".git" {
		cwd = filepath.Dir(cwd)
	}
	p, err := proc.StartDetached(exe, []string{"daemon", "--git-dir", c.GitDir}, cwd, logf)
	if err != nil {
		return err
	}
	return p.Release()
}

// Do sends one request. If the daemon couldn't be reached, it reconnects (starting a new daemon
// when Spawn is set) and tries once more. Once a request has been sent it is never resent: the
// daemon may have acted on it, and a second send would happen twice.
func (c *Client) Do(ctx context.Context, req proto.Request) (proto.Response, error) {
	resp, sent, err := c.roundTrip(ctx, req)
	if err == nil || sent || ctx.Err() != nil {
		return resp, err
	}
	if cerr := c.connect(ctx); cerr != nil {
		return proto.Response{}, fmt.Errorf("%v (reconnecting: %v)", err, cerr)
	}
	resp, _, err = c.roundTrip(ctx, req)
	return resp, err
}

func dial(ctx context.Context, addr string) (net.Conn, error) {
	var dialer net.Dialer
	dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return dialer.DialContext(dctx, "tcp", addr)
}

// roundTrip sends one request and reads the response. sent reports whether the request was
// written, after which the daemon may have acted on it.
func (c *Client) roundTrip(ctx context.Context, req proto.Request) (resp proto.Response, sent bool, err error) {
	info := c.current()
	if info.Addr == "" {
		return proto.Response{}, false, ErrNoDaemon
	}
	conn, err := dial(ctx, info.Addr)
	if err != nil {
		return proto.Response{}, false, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(2 * time.Minute))
	}
	req.Token = info.Token
	if err := writeLine(conn, req); err != nil {
		return proto.Response{}, true, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return proto.Response{}, true, err
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return proto.Response{}, true, err
	}
	return resp, true, nil
}

// Hello registers a session and returns the connection that keeps it registered. The session
// lasts until the connection closes.
func (c *Client) Hello(ctx context.Context, h proto.Hello) (net.Conn, proto.Response, error) {
	if !c.load() {
		if err := c.connect(ctx); err != nil {
			return nil, proto.Response{}, err
		}
	}
	conn, err := dial(ctx, c.current().Addr)
	if err != nil {
		if cerr := c.connect(ctx); cerr != nil {
			return nil, proto.Response{}, cerr
		}
		if conn, err = dial(ctx, c.current().Addr); err != nil {
			return nil, proto.Response{}, err
		}
	}
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := writeLine(conn, proto.Request{Token: c.current().Token, Op: proto.OpHello, Hello: &h}); err != nil {
		conn.Close()
		return nil, proto.Response{}, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		conn.Close()
		return nil, proto.Response{}, err
	}
	conn.SetDeadline(time.Time{})
	var resp proto.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		conn.Close()
		return nil, proto.Response{}, err
	}
	if !resp.OK {
		conn.Close()
		return nil, resp, errors.New(resp.Error)
	}
	return conn, resp, nil
}

func writeLine(conn net.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = conn.Write(append(b, '\n'))
	return err
}
