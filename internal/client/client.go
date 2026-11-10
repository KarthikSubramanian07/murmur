// Package client finds a clone's daemon, starting it if asked, and talks to it.
package client

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/gitx"
	"github.com/a-Fig/murmur/internal/logfile"
	"github.com/a-Fig/murmur/internal/proc"
	"github.com/a-Fig/murmur/internal/proto"
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
	// MaySpawn, if set, is asked each time a daemon would be started, and says no to stop it. A
	// client that outlives the user's consent (the MCP server) needs it: it learns the consent is
	// gone when the daemon it talked to goes away, which is the moment it would start another.
	MaySpawn func() bool

	// launch starts a daemon; start, unless a test says otherwise.
	launch func() error

	mu   sync.Mutex
	info proto.Info
	// older says the daemon that last answered a ping speaks an older protocol than this binary.
	older bool
	// legacy says that daemon is from before proof (proto.ProofProtocol): it can't prove itself,
	// and is trusted only while it holds the clone's lock.
	legacy bool
}

func (c *Client) current() proto.Info {
	info, _ := c.target()
	return info
}

// target is the daemon c talks to, and whether it is from before proof.
func (c *Client) target() (proto.Info, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info, c.legacy
}

// ErrNoPage means the daemon is running but isn't serving the page for humans.
var ErrNoPage = errors.New("the daemon isn't serving the page")

// PageURL is the address that opens the page in a browser. The page's token goes in the
// fragment, which a browser never sends to a server, and the page keeps it for its own requests.
// It is not the daemon's token: the address reaches the browser's history and extensions, and
// the daemon's lets whoever holds it send as this device (#42). A daemon from before the page had
// a token of its own takes the daemon's.
func (c *Client) PageURL() (string, error) {
	info := c.current()
	if info.Web == "" {
		return "", ErrNoPage
	}
	token := info.PageToken
	if token == "" {
		token = info.Token
	}
	return "http://" + info.Web + "/#token=" + token, nil
}

// PageKeyURL is an address that opens the page once, for another clone's page to send its tab to
// (#348): a one-time key in the fragment, which the page trades for its token, so the token itself
// never travels in an address.
func (c *Client) PageKeyURL(ctx context.Context) (string, error) {
	resp, err := c.Do(ctx, proto.Request{Op: proto.OpPageKey})
	if err == nil && !resp.OK {
		err = errors.New(resp.Error)
	}
	if err != nil {
		return "", err
	}
	info := c.current() // the daemon that answered: Do reloads it if it had to reconnect
	if info.Web == "" || resp.PageKey == "" {
		return "", ErrNoPage
	}
	return "http://" + info.Web + "/#key=" + resp.PageKey, nil
}

// Running reports whether a daemon answers for gitDir, without starting one or sending it the
// token: what answers at its port is asked only to prove itself.
func Running(ctx context.Context, gitDir string) bool {
	_, _, _, ok := (&Client{GitDir: gitDir}).running(ctx)
	return ok
}

// Connect finds the daemon for gitDir, starting one if spawn is set and none answers.
func Connect(ctx context.Context, gitDir string, spawn bool) (*Client, error) {
	c := &Client{GitDir: gitDir, Spawn: spawn}
	return c, c.connect(ctx)
}

// connect finds the clone's daemon. One that speaks an older protocol than this binary (left running
// by the mur it replaced) is stopped and replaced if the client may start daemons; a client that
// may not, such as `mur stop`, which has to be able to stop it, takes it as it is.
func (c *Client) connect(ctx context.Context) error {
	if c.found(ctx) {
		if !c.Outdated() || !c.mayStart() {
			return nil
		}
		if err := c.replace(ctx); err != nil {
			return err
		}
	} else if !c.mayStart() {
		return ErrNoDaemon
	}
	launch := c.launch
	if launch == nil {
		launch = c.start
	}
	if err := launch(); err != nil {
		return fmt.Errorf("starting the daemon: %w", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
		if c.found(ctx) && !c.Outdated() {
			return nil
		}
	}
	return fmt.Errorf("the daemon didn't come up; see %s", filepath.Join(StateDir(c.GitDir), "daemon.log"))
}

func (c *Client) mayStart() bool {
	return c.Spawn && (c.MaySpawn == nil || c.MaySpawn())
}

// Outdated reports whether the daemon that last answered speaks an older protocol than this binary:
// one that Connect found and, without permission to start daemons, left running.
func (c *Client) Outdated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.older
}

// StopWait is how long to wait for a daemon asked to stop to let go of the clone: as long as a
// shutdown may take (draining its work, then saying goodbye, with the lock held throughout), and a
// little more for the request to arrive and the process to go. replace waits this long, and so does
// `join --leave` (#91); a caller that gives up sooner ends the wait sooner.
const StopWait = proto.ShutdownMax + 5*time.Second

// replaceWait is StopWait, as a variable for the test.
var replaceWait = StopWait

// replace stops the daemon at the address c has, which is outdated, and waits until it has let go of
// the clone, so that the daemon started next finds the lock free. Another client may be doing the
// same: if the daemon.json that appears isn't the old one's, the work is done.
func (c *Client) replace(ctx context.Context) error {
	old := c.current()
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	c.roundTrip(sctx, proto.Request{Op: proto.OpStop}) // by the old address and token, never a newer daemon's
	cancel()
	lock := filepath.Join(StateDir(c.GitDir), "daemon.lock")
	for end := time.Now().Add(replaceWait); ; {
		if info, ok := c.load(); ok && info.Token != old.Token {
			return nil
		}
		l, err := proc.LockShared(lock)
		if err == nil {
			l.Close()
			return nil
		}
		if !errors.Is(err, proc.ErrLocked) {
			return fmt.Errorf("replacing the daemon of an older version: %w", err)
		}
		if time.Now().After(end) {
			return fmt.Errorf("the daemon of an older version (pid %d) didn't exit when asked to", old.PID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// found reports whether daemon.json names a running daemon, and if so makes it the one c talks to.
// What answers at its address must prove it has the token daemon.json names (proto.Proof), and the
// clone's lock must be held. The proof is enough by itself; the lock is all a daemon from before it
// (protocol 11 and older) has to show, and was how #312 first told a daemon from an impostor. A daemon takes the
// lock before it writes daemon.json, and removes the file before it gives up its port and long
// before it lets the lock go, so a daemon.json beside a free lock is stale: its daemon died without
// removing it (a panic, SIGKILL, the OOM killer, a power cut), and whatever answers at its port
// isn't that daemon. On a machine with other users it can be one of their processes, which would be
// sent the sessions' hellos and requests, and could answer them with messages of its own. Another
// user can't hold the lock where the state directory is private to its user: on Unix it is made
// 0700, and on Windows the daemon gives it an access list that lets in only the user, SYSTEM and
// Administrators (on a local volume that keeps access lists).
//
// The lock is tried only after something answered, as holding it for a moment can turn away a
// daemon starting just then. Clients (another one here, WaitGone, replace) hold it for a moment
// too, but only shared, and only a daemon takes it exclusively; so a client's shared lock is
// refused only by a daemon, however long another client holds its own. Then daemon.json is read
// again: a daemon that starts beside a stale daemon.json removes it once it
// holds the lock, so a client that read the stale one just before doesn't take it for the new
// daemon's.
//
// When none is found, c forgets the daemon it had. That one is gone, and a long-running client (the
// MCP server) would otherwise go on sending its requests to the address while a new daemon starts.
func (c *Client) found(ctx context.Context) bool {
	info, protocol, legacy, ok := c.running(ctx)
	c.mu.Lock()
	c.info, c.legacy, c.older = info, legacy, ok && protocol < proto.Protocol
	c.mu.Unlock()
	return ok
}

// running is found's check: the daemon daemon.json names, its protocol number, and whether it is
// from before proof, if it is running.
func (c *Client) running(ctx context.Context) (info proto.Info, protocol int, legacy bool, ok bool) {
	info, ok = c.load()
	if !ok {
		return proto.Info{}, 0, false, false
	}
	protocol, perr := ping(ctx, info)
	legacy = errors.Is(perr, errLegacy)
	// Something that answered and couldn't prove itself isn't the daemon, but the lock still says
	// whether daemon.json is stale.
	if perr != nil && !legacy && !errors.Is(perr, errNotDaemon) {
		return proto.Info{}, 0, false, false
	}
	dir := StateDir(c.GitDir)
	l, err := proc.LockShared(filepath.Join(dir, "daemon.lock"))
	if err == nil {
		os.Remove(filepath.Join(dir, "daemon.json")) // stale, and no daemon can start to write one while this is held
		l.Close()
		return proto.Info{}, 0, false, false
	}
	if perr != nil && !legacy {
		return proto.Info{}, 0, false, false
	}
	if !errors.Is(err, proc.ErrLocked) {
		return proto.Info{}, 0, false, false
	}
	if again, ok := c.load(); !ok || again != info {
		return proto.Info{}, 0, false, false
	}
	return info, protocol, legacy, true
}

// held reports whether a daemon holds the clone's lock and daemon.json still names info: all that a
// daemon from before proof can show, checked before each connection to one. It narrows, and can't
// close, the window in which such a daemon dies and another process takes its port; a client that
// may start daemons replaces one of these as soon as it finds it.
func (c *Client) held(info proto.Info) bool {
	l, err := proc.LockShared(filepath.Join(StateDir(c.GitDir), "daemon.lock"))
	if err == nil {
		l.Close()
		return false
	}
	if !errors.Is(err, proc.ErrLocked) {
		return false
	}
	again, ok := c.load()
	return ok && again == info
}

// load reads daemon.json.
func (c *Client) load() (proto.Info, bool) {
	b, err := os.ReadFile(filepath.Join(StateDir(c.GitDir), "daemon.json"))
	if err != nil {
		return proto.Info{}, false
	}
	var info proto.Info
	if json.Unmarshal(b, &info) != nil || info.Addr == "" || info.Token == "" {
		return proto.Info{}, false
	}
	return info, true
}

// ping asks what answers at info's address to prove it is info's daemon, and returns its protocol
// number. It sends no token, and reads at most maxProof of the answer: until it is proved, what
// answers may be anyone's. errLegacy is a daemon from before proof, or something that answers as
// one does; the caller decides what to make of that.
func ping(ctx context.Context, info proto.Info) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	conn, stop, err := open(ctx, info.Addr)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	defer stop()
	protocol, err := prove(conn, bufio.NewReader(conn), info)
	if err != nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	return protocol, err
}

// errNotDaemon is what answers at the daemon's address failing to prove it is the daemon.
var errNotDaemon = errors.New("what answers at the daemon's address couldn't prove it is the daemon")

// errLegacy is a refusal of the proof's ping for its missing token: what a daemon from before
// proof answers, and what anything else can answer too.
var errLegacy = errors.New("the daemon is from before proof")

// errTooLong is a reply longer than a client reads.
var errTooLong = errors.New("the daemon's reply is too long")

// maxProof caps the reply to the proof's ping, the first thing read from a listener that may not be
// the daemon. It is a hundred bytes or so.
const maxProof = 4 << 10

// maxReply caps a proved daemon's reply. The longest is an inbox: at most 200 messages (the
// daemon's maxInbox) of at most 32 KB (bus.MaxBody), which JSON escaping can make six times longer,
// about 38 MB. A variable for the test.
var maxReply = 64 << 20

// prove sends the proof's ping on conn, and reads and checks its reply from r.
func prove(conn net.Conn, r *bufio.Reader, info proto.Info) (int, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return 0, err
	}
	nonce := hex.EncodeToString(b)
	if err := writeLine(conn, proto.Request{Op: proto.OpPing, Nonce: nonce}); err != nil {
		return 0, err
	}
	line, err := readLine(r, maxProof)
	if err != nil {
		if errors.Is(err, errTooLong) {
			err = errNotDaemon
		}
		return 0, err
	}
	var resp proto.Response
	if json.Unmarshal(line, &resp) != nil {
		return 0, errNotDaemon
	}
	if resp.Proof == "" && !resp.OK && resp.Error == "bad token" {
		return 0, errLegacy
	}
	if !hmac.Equal([]byte(resp.Proof), []byte(proto.Proof(info.Token, info.Addr, nonce))) {
		return 0, errNotDaemon
	}
	return resp.Protocol, nil
}

// readLine reads a line of at most max bytes, its newline included, and never holds more than that
// and a buffer's worth.
func readLine(r *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	for {
		part, err := r.ReadSlice('\n')
		if len(line)+len(part) > max {
			return nil, errTooLong
		}
		line = append(line, part...)
		if err != bufio.ErrBufferFull {
			return line, err
		}
	}
}

// start launches a daemon detached from this process. If two clients start one at once, the
// second daemon finds the lock taken and exits.
func (c *Client) start() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// A running MCP server can outlive an update. Start the installed path rather
	// than the renamed Windows backup (or Linux's deleted executable name).
	exe = installedExecutable(exe)
	return StartDaemon(exe, c.GitDir, nil)
}

// StartDaemon starts the installed executable after a self-update has released the clone lock.
// Forwarded flags use --name=value, as recorded from the daemon's parsed FlagSet. page, if not nil,
// is the page's listening socket, which the new daemon inherits so that the port is never free
// between the two (#255).
func StartDaemon(exe, gitDir string, page *os.File, flags ...string) error {
	dir := StateDir(gitDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Before the log is made in it. The daemon tries again, and logs a failure; this one is told
	// only if the log can't be opened, when it may be why.
	perr := proc.Protect(dir)
	logPath := filepath.Join(dir, "daemon.log")
	// WMI cannot inherit logf and adds its path as --log. Open the forwarded
	// log destination so that fallback preserves a custom --log too.
	for _, f := range flags {
		if path, ok := strings.CutPrefix(f, "--log="); ok && path != "" {
			logPath = path
		}
	}
	if fi, err := os.Stat(logPath); err == nil && fi.Size() > logfile.Max {
		os.Rename(logPath, logPath+".old")
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		if perr != nil {
			return fmt.Errorf("%w (keeping other users out of %s: %v)", err, dir, perr)
		}
		return err
	}
	defer logf.Close()
	// Run in the clone's top directory, where git resolves a relative remote URL from. The
	// daemon still never uses it as a working tree: every command it runs gets --git-dir.
	cwd := gitDir
	if filepath.Base(cwd) == ".git" {
		cwd = filepath.Dir(cwd)
	}
	args := append([]string{"daemon"}, flags...)
	// The launcher's cwd may differ from the original daemon's. The resolved
	// clone directory wins over a forwarded relative --git-dir.
	args = append(args, "--git-dir", gitDir)
	var hand *proc.Handover
	if page != nil {
		hand = &proc.Handover{File: page, Env: brand.PageListenerEnv}
	}
	p, err := proc.StartDetached(exe, args, cwd, logf, hand)
	if err != nil {
		return err
	}
	// A nil process means the daemon was started and had already exited; connect polls for it.
	if p == nil {
		return nil
	}
	return p.Release()
}

// Stop asks the clone's daemon to exit and waits for it to go. It reports whether there was one.
func Stop(ctx context.Context, gitDir string) (bool, error) {
	c, err := Connect(ctx, gitDir, false)
	if errors.Is(err, ErrNoDaemon) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	resp, err := c.Do(ctx, proto.Request{Op: proto.OpStop})
	if err == nil && resp.Error != "" {
		err = errors.New(resp.Error)
	}
	if err != nil {
		return true, err
	}
	// The daemon removes daemon.json as soon as it begins to stop, and holds the clone's lock, and
	// files, until it has gone. A daemon still there after this wait is left to finish: it takes no
	// more requests.
	WaitGone(ctx, gitDir, 10*time.Second)
	return true, nil
}

// ErrStillRunning is what WaitGone returns when a daemon still holds the clone's lock at the deadline.
var ErrStillRunning = errors.New("the daemon is still running")

// WaitGone waits, up to d, until no daemon holds the clone's lock, and reports whether one did when
// it began. A daemon still starting has no daemon.json yet, so Stop doesn't see it; it does hold the
// lock. If one still does at the deadline the error is ErrStillRunning, so a caller can't take a
// slow daemon for a gone one.
func WaitGone(ctx context.Context, gitDir string, d time.Duration) (held bool, err error) {
	for end := time.Now().Add(d); ; time.Sleep(50 * time.Millisecond) {
		l, lerr := proc.LockShared(filepath.Join(StateDir(gitDir), "daemon.lock"))
		if lerr == nil {
			l.Close()
			return held, nil
		}
		if errors.Is(lerr, fs.ErrNotExist) {
			return held, nil // no state directory: no daemon has ever run here
		}
		if !errors.Is(lerr, proc.ErrLocked) {
			return held, lerr
		}
		held = true
		if ctx.Err() != nil {
			return held, ctx.Err()
		}
		if time.Now().After(end) {
			return held, ErrStillRunning
		}
	}
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

// open dials addr for one exchange, with ctx's deadline (or two minutes) on the connection.
// Cancelling ctx ends a write or read already under way: a deadline in the past makes both return
// at once. The MCP server cancels a call when its session moves to another repo or leaves, and the
// call must not reach the old repo's daemon, or wait on it, after that. stop undoes that link.
func open(ctx context.Context, addr string) (conn net.Conn, stop func() bool, err error) {
	conn, err = dial(ctx, addr)
	if err != nil {
		return nil, nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(2 * time.Minute))
	}
	return conn, context.AfterFunc(ctx, func() { conn.SetDeadline(time.Unix(1, 0)) }), nil
}

// roundTrip sends one request and reads the response. sent reports whether the request was
// written, after which the daemon may have acted on it.
func (c *Client) roundTrip(ctx context.Context, req proto.Request) (resp proto.Response, sent bool, err error) {
	info, legacy := c.target()
	if legacy && !c.held(info) {
		return proto.Response{}, false, errNotDaemon
	}
	return exchange(ctx, info, legacy, req)
}

// exchange is roundTrip with the daemon info names. Each connection proves the daemon before the
// request goes, on that connection, so a request never reaches whatever took the port of a daemon
// that died since it was found (#371); only one from before proof (legacy) is sent to unproved.
func exchange(ctx context.Context, info proto.Info, legacy bool, req proto.Request) (resp proto.Response, sent bool, err error) {
	if info.Addr == "" {
		return proto.Response{}, false, ErrNoDaemon
	}
	conn, stop, err := open(ctx, info.Addr)
	if err != nil {
		return proto.Response{}, false, err
	}
	defer conn.Close()
	defer stop()
	if ctx.Err() != nil {
		return proto.Response{}, false, ctx.Err()
	}
	r := bufio.NewReader(conn)
	if !legacy {
		if _, err := prove(conn, r, info); err != nil {
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			return proto.Response{}, false, err
		}
	}
	req.Token = info.Token
	if err := writeLine(conn, req); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return proto.Response{}, true, err
	}
	line, err := readLine(r, maxReply)
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return proto.Response{}, true, err
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return proto.Response{}, true, err
	}
	return resp, true, nil
}

// openSession opens a connection to the daemon info names, for a hello, proving it first.
func (c *Client) openSession(ctx context.Context, info proto.Info, legacy bool) (net.Conn, *bufio.Reader, error) {
	if legacy && !c.held(info) {
		return nil, nil, errNotDaemon
	}
	conn, err := dial(ctx, info.Addr)
	if err != nil {
		return nil, nil, err
	}
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	r := bufio.NewReader(conn)
	if !legacy {
		if _, err := prove(conn, r, info); err != nil {
			conn.Close()
			return nil, nil, err
		}
	}
	return conn, r, nil
}

// Hello registers a session and returns the connection that keeps it registered. The session
// lasts until the connection closes.
func (c *Client) Hello(ctx context.Context, h proto.Hello) (net.Conn, proto.Response, error) {
	// Every registration pings first, which is also where a daemon of an older version is found.
	if err := c.connect(ctx); err != nil {
		return nil, proto.Response{}, err
	}
	// One read of the daemon for both its address and its token: a call on another goroutine can
	// change it in between, and one daemon's token must never go to another's address.
	info, legacy := c.target()
	conn, r, err := c.openSession(ctx, info, legacy)
	if err != nil {
		if cerr := c.connect(ctx); cerr != nil {
			return nil, proto.Response{}, cerr
		}
		info, legacy = c.target()
		if conn, r, err = c.openSession(ctx, info, legacy); err != nil {
			return nil, proto.Response{}, err
		}
	}
	if err := writeLine(conn, proto.Request{Token: info.Token, Op: proto.OpHello, Hello: &h}); err != nil {
		conn.Close()
		return nil, proto.Response{}, err
	}
	line, err := readLine(r, maxReply)
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
	return sessionConn{conn}, resp, nil
}

// sessionConn is a session's connection, whose Close shuts the socket down before closing it. A
// close alone ends only this process's descriptor: a child forked at that moment, not yet started
// as its program, holds a copy, and the daemon sees the session end only when that copy goes. In
// the daemon's tests one such child never got that far (#217), and the session never ended.
type sessionConn struct{ net.Conn }

func (c sessionConn) Close() error {
	if tc, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		tc.CloseWrite()
	}
	return c.Conn.Close()
}

func writeLine(conn net.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = conn.Write(append(b, '\n'))
	return err
}

func installedExecutable(exe string) string {
	exe = strings.TrimSuffix(exe, " (deleted)")
	base := filepath.Base(exe)
	if i := strings.LastIndex(base, ".old-.mur-update-"); i >= 0 {
		exe = filepath.Join(filepath.Dir(exe), base[:i])
	}
	return exe
}
