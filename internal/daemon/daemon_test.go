package daemon_test

import (
	"context"
	"io"
	"log"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a-Fig/t4/internal/client"
	"github.com/a-Fig/t4/internal/daemon"
	"github.com/a-Fig/t4/internal/proto"
)

// These tests run two devices (two clones of one bare remote, each with its own daemon) in one
// process, with fast timings. Everything goes through real git.

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newRemote(t *testing.T) string {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	git(t, root, "init", "-q", "--bare", "--initial-branch=main", remote)
	seed := filepath.Join(root, "seed")
	git(t, root, "init", "-q", "--initial-branch=main", seed)
	git(t, seed, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	git(t, seed, "push", "-q", remote, "HEAD:refs/heads/main")
	return remote
}

type device struct {
	t      *testing.T
	gitDir string
	c      *client.Client
	stop   func()
}

func newDevice(t *testing.T, remote string) *device {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clone")
	git(t, filepath.Dir(dir), "clone", "-q", remote, dir)
	d := &device{t: t, gitDir: filepath.Join(dir, ".git")}
	d.start()
	return d
}

func (d *device) start() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := daemon.Run(ctx, daemon.Options{GitDir: d.gitDir, Poll: 100 * time.Millisecond,
			Heartbeat: 500 * time.Millisecond, StaleAfter: 3 * time.Second, Idle: -1,
			Log: log.New(io.Discard, "", 0)})
		if err != nil {
			d.t.Errorf("daemon: %v", err)
		}
	}()
	var once sync.Once
	d.stop = func() { once.Do(func() { cancel(); <-done }) }
	d.t.Cleanup(d.stop)
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := client.Connect(context.Background(), d.gitDir, false)
		if err == nil {
			d.c = c
			return
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("daemon didn't come up: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (d *device) do(req proto.Request) proto.Response {
	d.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := d.c.Do(ctx, req)
	if err != nil {
		d.t.Fatalf("%s: %v", req.Op, err)
	}
	return resp
}

// session registers a session and returns its address and the connection keeping it alive.
func (d *device) session(id, claude string) (string, net.Conn) {
	d.t.Helper()
	conn, resp, err := d.c.Hello(context.Background(), proto.Hello{ID: id, Agent: "test", ClaudeSession: claude})
	if err != nil {
		d.t.Fatalf("hello %s: %v", id, err)
	}
	d.t.Cleanup(func() { conn.Close() })
	return resp.Self, conn
}

func (d *device) self() string {
	return strings.TrimSuffix(d.do(proto.Request{Op: proto.OpPing}).Self, "/"+proto.CLISession)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestMessagesNoticesInbox(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	alice, _ := a.session("aaaaaa", "")
	bob, _ := b.session("bbbbbb", "claude-bob")

	// Wait until each device sees the other's session in its presence.
	sees := func(d *device, addr string) bool {
		for _, dv := range d.do(proto.Request{Op: proto.OpWho}).Devices {
			for _, s := range dv.Sessions {
				if s.Addr == addr {
					return true
				}
			}
		}
		return false
	}
	eventually(t, "presence", func() bool { return sees(a, bob) && sees(b, alice) })

	if r := a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: bob, Subject: "auth middleware?", Body: "Are you changing it?"}); r.Error != "" || r.Note != "" {
		t.Fatalf("send: %+v", r)
	}
	var notices []proto.MessageView
	eventually(t, "notice", func() bool {
		notices = b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-bob"}).Messages
		return len(notices) > 0
	})
	if n := notices[0]; n.From != alice || n.Subject != "auth middleware?" || n.Body != "" || n.FromAgent != "test" {
		t.Fatalf("notice: %+v", n)
	}
	if again := b.do(proto.Request{Op: proto.OpNotices, ClaudeSession: "claude-bob"}).Messages; len(again) != 0 {
		t.Fatalf("notice repeated: %+v", again)
	}
	inbox := b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages
	if len(inbox) != 1 || inbox[0].Body != "Are you changing it?" {
		t.Fatalf("inbox: %+v", inbox)
	}
	if again := b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages; len(again) != 0 {
		t.Fatalf("inbox not emptied: %+v", again)
	}

	// A broadcast reaches every other session, including the other device's CLI, but not the sender.
	a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: "*", Subject: "using zustand for state"})
	eventually(t, "broadcast", func() bool {
		return len(b.do(proto.Request{Op: proto.OpInbox}).Messages) == 1
	})
	if got := b.do(proto.Request{Op: proto.OpInbox, Session: "bbbbbb"}).Messages; len(got) != 1 {
		t.Fatalf("session missed broadcast: %+v", got)
	}
	if got := a.do(proto.Request{Op: proto.OpInbox, Session: "aaaaaa"}).Messages; len(got) != 0 {
		t.Fatalf("sender got own broadcast: %+v", got)
	}

	if r := a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: bob, Subject: "key", Body: "AKIAIOSFODNN7EXAMPLE"}); !strings.Contains(r.Error, "not sent") {
		t.Fatalf("secret not blocked: %+v", r)
	}
}

func TestClaimsCompareAndSwap(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	a.session("aaaaaa", "")
	_, bobConn := b.session("bbbbbb", "")

	// Both claim at once, each having seen nothing. Exactly one may win.
	var wg sync.WaitGroup
	results := make([]proto.Response, 2)
	for i, d := range []*device{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess := []string{"aaaaaa", "bbbbbb"}[i]
			results[i] = d.do(proto.Request{Op: proto.OpClaim, Session: sess, What: "auth flow"})
		}()
	}
	wg.Wait()
	won := 0
	var loser int
	for i, r := range results {
		if r.Error != "" {
			t.Fatalf("claim %d: %s", i, r.Error)
		}
		if r.Claimed != nil {
			won++
		} else {
			loser = i
		}
	}
	if won != 1 {
		t.Fatalf("%d claims won: %+v", won, results)
	}
	lr := results[loser]
	if len(lr.Unseen) != 1 || lr.Unseen[0].What != "auth flow" {
		t.Fatalf("loser didn't get the winning claim back: %+v", lr)
	}

	// With the winner in view, the loser claims something else using the token it was given.
	sess := []string{"aaaaaa", "bbbbbb"}[loser]
	d := []*device{a, b}[loser]
	r := d.do(proto.Request{Op: proto.OpClaim, Session: sess, What: "billing page", Seen: lr.Seen})
	if r.Claimed == nil {
		t.Fatalf("second claim: %+v", r)
	}
	if open := a.do(proto.Request{Op: proto.OpClaims}).Claims; len(open) != 2 {
		t.Fatalf("open claims: %+v", open)
	}

	// The winner claims again with an old token: it is shown the loser's newer claim, and its own
	// claim doesn't count against it.
	winner := 1 - loser
	ws := []string{"aaaaaa", "bbbbbb"}[winner]
	r = []*device{a, b}[winner].do(proto.Request{Op: proto.OpClaim, Session: ws, What: "search"})
	if r.Claimed != nil || len(r.Unseen) != 1 || r.Unseen[0].What != "billing page" {
		t.Fatalf("stale token: %+v", r)
	}

	// Bob's session ends: his claims are released, and a sees them go.
	bobAddr := b.self() + "/bbbbbb"
	bobConn.Close()
	eventually(t, "bob's claims released", func() bool {
		for _, c := range a.do(proto.Request{Op: proto.OpClaims}).Claims {
			if c.By == bobAddr {
				return false
			}
		}
		return true
	})
}

func TestClaimFromCLILapsesWithDaemon(t *testing.T) {
	remote := newRemote(t)
	a, b := newDevice(t, remote), newDevice(t, remote)
	if r := b.do(proto.Request{Op: proto.OpClaim, What: "landing page"}); r.Claimed == nil {
		t.Fatalf("cli claim: %+v", r)
	}
	eventually(t, "a sees the claim", func() bool { return len(a.do(proto.Request{Op: proto.OpClaims}).Claims) == 1 })
	b.stop() // its goodbye removes its presence
	eventually(t, "the claim lapses", func() bool { return len(a.do(proto.Request{Op: proto.OpClaims}).Claims) == 0 })
}

func TestHistoryIsNotReplayed(t *testing.T) {
	remote := newRemote(t)
	a := newDevice(t, remote)
	a.do(proto.Request{Op: proto.OpSend, To: "*", Subject: "sent before b existed"})
	b := newDevice(t, remote)
	eventually(t, "b's first sync", func() bool { return len(b.do(proto.Request{Op: proto.OpWho}).Devices) == 2 })
	a.do(proto.Request{Op: proto.OpSend, To: "*", Subject: "sent after"})
	var got []proto.MessageView
	eventually(t, "the new message", func() bool {
		got = append(got, b.do(proto.Request{Op: proto.OpInbox}).Messages...)
		return len(got) > 0
	})
	if len(got) != 1 || got[0].Subject != "sent after" {
		t.Fatalf("got %+v", got)
	}
}

func TestBusNeverTouchesWorkingTree(t *testing.T) {
	remote := newRemote(t)
	a := newDevice(t, remote)
	work := filepath.Dir(a.gitDir)
	before := git(t, work, "status", "--porcelain", "--ignored")
	head := git(t, work, "rev-parse", "HEAD")
	a.session("aaaaaa", "")
	a.do(proto.Request{Op: proto.OpSend, Session: "aaaaaa", To: "*", Subject: "hello"})
	a.do(proto.Request{Op: proto.OpClaim, Session: "aaaaaa", What: "x"})
	if after := git(t, work, "status", "--porcelain", "--ignored"); after != before {
		t.Fatalf("working tree changed:\n%s", after)
	}
	if h := git(t, work, "rev-parse", "HEAD"); h != head {
		t.Fatalf("HEAD moved")
	}
	// Bus commits carry the empty tree.
	tree := git(t, work, "log", "-1", "--format=%T", "refs/t4/claims")
	if empty := git(t, work, "hash-object", "-t", "tree", "--stdin"); tree != empty {
		t.Fatalf("claims commit tree %s, want empty %s", tree, empty)
	}
}
