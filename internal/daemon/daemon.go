// Package daemon is the per-clone process that owns the bus for its device: the only process
// that polls the remote and the only one that writes this device's refs. Sessions on the device
// reach it through package client.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/a-Fig/t4/internal/brand"
	"github.com/a-Fig/t4/internal/bus"
	"github.com/a-Fig/t4/internal/gitx"
	"github.com/a-Fig/t4/internal/proc"
	"github.com/a-Fig/t4/internal/proto"
)

// Options configure a daemon. Zero values take the defaults in docs/VISION.md.
type Options struct {
	GitDir     string        // the clone's git common dir, absolute
	Poll       time.Duration // how often to check the remote; 10 s
	Heartbeat  time.Duration // how often to re-push presence when nothing changed; 2 min
	StaleAfter time.Duration // presence unchanged this long means the device is gone; 6 min
	Idle       time.Duration // exit after this long with no sessions; 10 min. Negative: never
	// SessionGrace is how long a session whose connection dropped stays registered, for the same
	// session ID to take it over: 60 s. Its MCP server restarts while the conversation carries on.
	SessionGrace time.Duration
	// StoppedInbox is how long a stopped session's inbox is kept for it to come back to: 24 h.
	StoppedInbox time.Duration
	Log          *log.Logger
}

func (o *Options) defaults() {
	if o.Poll == 0 {
		o.Poll = 10 * time.Second
	}
	if o.Heartbeat == 0 {
		o.Heartbeat = 2 * time.Minute
	}
	if o.StaleAfter == 0 {
		o.StaleAfter = 6 * time.Minute
	}
	if o.Idle == 0 {
		o.Idle = 10 * time.Minute
	}
	if o.SessionGrace == 0 {
		o.SessionGrace = time.Minute
	}
	if o.StoppedInbox == 0 {
		o.StoppedInbox = 24 * time.Hour
	}
	if o.Log == nil {
		o.Log = log.New(os.Stderr, "", log.LstdFlags)
	}
}

// ErrRunning means another daemon already holds this clone's lock.
var ErrRunning = errors.New("a daemon is already running for this clone")

// maxInbox caps each session's unread messages; the oldest go first.
const maxInbox = 200

// Daemon is one running daemon. Lock order: syncMu, then advMu, then mu; or writeMu, then advMu,
// then mu.
type Daemon struct {
	opt     Options
	repo    *bus.Repo
	dir     string
	device  string
	host    string
	owner   string
	token   string
	started time.Time
	log     *log.Logger

	mu         sync.Mutex
	sessions   map[string]*session
	peers      map[string]*peer
	known      map[string]string // local copy of each bus ref: ref → SHA last fetched
	st         state
	recent     []proto.EventView
	lastActive time.Time
	mode       syncMode
	pending    []bus.Event     // what arrived while down, for sessions that reconnect
	caughtUp   map[string]bool // sessions already given pending
	// backlog lists the mesh refs the first listing showed that haven't been read yet: they are
	// read under the mode the daemon started in, and the mode turns normal once all have been.
	// nil until that listing.
	backlog    map[string]bool
	backlogAt  time.Time            // when the backlog was taken
	unfetched  map[string]fetchFail // refs whose fetch failed, and when
	junkTried  map[string]time.Time // blocking refs clearOwnJunk last tried to delete, and when
	lastPollOK time.Time            // when the last successful sync listed the remote (monotonic)

	syncMu  sync.Mutex // serializes syncs, so an older listing is never applied after a newer one
	advMu   sync.Mutex // serializes processing of fetched refs
	writeMu sync.Mutex // serializes appends to this device's log
	ownTip  string     // the remote's tip of this device's log, as last known; under writeMu

	kick     chan struct{}
	quit     chan struct{}
	quitOnce sync.Once

	// bg tracks connection handlers and background writes, so Run returns only after they
	// have; bgCtx is cancelled when the daemon starts shutting down.
	bg    sync.WaitGroup
	bgCtx context.Context
}

type session struct {
	id, agent, cwd, branch, doing, claudeSession string
	started                                      time.Time
	reconnect                                    bool // registered before, with a daemon that went away
	// since is, for a session back within StoppedInbox, when it stopped: what was sent after that
	// is its, however late this daemon reads it. Zero otherwise.
	since time.Time
	// dropped is when the session's connection dropped, zero while it is connected. Until the
	// grace runs out the session stays registered, and a hello with its ID takes it over; that
	// closes takeover, which the connection waiting out the grace is watching.
	dropped  time.Time
	takeover chan struct{}
}

// syncMode says what a sync does with events this daemon hasn't processed.
type syncMode int

const (
	// modeSkip records every ref's tip without delivering anything: the first sync of a new
	// clone, or after a long gap, when no session is still waiting for what it missed.
	modeSkip syncMode = iota
	// modeCatchUp delivers what arrived while the daemon was down, but only to sessions that
	// were running before it went down and reconnected: the first sync after a restart.
	modeCatchUp
	modeNormal
)

// catchUpLimit is the longest outage after which a restarted daemon still delivers what it
// missed; past it, the sessions that were waiting have moved on. catchUpWindow is how long after
// starting it holds what it missed for sessions still reconnecting.
const (
	catchUpLimit  = 10 * time.Minute
	catchUpWindow = 30 * time.Second
)

// peer is another device as its presence ref shows it.
type peer struct {
	pres bus.Presence
	sha  string // the presence last read, or found unreadable
	// listed is the presence SHA last seen in a listing, read or not. lastChange is when this
	// daemon saw it change, on this machine's clock, so that staleness never compares two
	// machines' clocks.
	listed     string
	lastChange time.Time
}

// fetchFail is a ref whose fetch failed: at which listed SHA, and when. It is fetched again on
// its own a minute after at; a zero at means at the next poll.
type fetchFail struct {
	sha string
	at  time.Time
}

type inMsg struct {
	Event   bus.Event `json:"event"`
	Arrived time.Time `json:"arrived"`
	Noticed bool      `json:"noticed"`
}

// state is what survives a restart, in state.json.
type state struct {
	// Initialized is set once the first syncs have read the backlog, recording every log's tip
	// as processed without delivering it: history from before this device joined isn't replayed.
	Initialized bool              `json:"initialized"`
	Processed   map[string]string `json:"processed"` // mesh ref → last SHA delivered
	// Junked holds the logs that were junk when this device first looked, with nothing read from
	// them yet, and when that look was: what was sent before it is history, not to be delivered.
	Junked map[string]time.Time `json:"junked,omitempty"`
	Inbox  map[string][]*inMsg  `json:"inbox"` // session → unread messages
	// Stopped holds the sessions that stopped, and when. Their inboxes are kept for StoppedInbox,
	// in case the session comes back; they are the only inboxes kept for sessions not running.
	Stopped map[string]time.Time `json:"stopped,omitempty"`
	// LastSync tells the next daemon how long this one was down.
	LastSync time.Time `json:"lastSync"`
}

// Run runs a daemon until ctx is done, it is told to stop, or it has been idle too long.
func Run(ctx context.Context, opt Options) error {
	opt.defaults()
	dir := filepath.Join(opt.GitDir, brand.StateDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lock, err := proc.Lock(filepath.Join(dir, "daemon.lock"))
	if errors.Is(err, proc.ErrLocked) {
		return ErrRunning
	}
	if err != nil {
		return err
	}
	defer lock.Close()

	d, err := open(ctx, opt, dir)
	if err != nil {
		return err
	}
	// Sync once before accepting sessions, so whatever this sync finds was sent before any of
	// them registered: it is the backlog, and nothing a new session should get.
	sctx, scancel := context.WithTimeout(ctx, 10*time.Second)
	if err := d.pollOnce(sctx); err != nil {
		d.log.Printf("first sync failed; retrying in the background: %v", err)
	}
	scancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer ln.Close()
	infoPath := filepath.Join(dir, "daemon.json")
	info := proto.Info{Addr: ln.Addr().String(), Token: d.token, PID: os.Getpid(), Device: d.device}
	if err := writeJSON(infoPath, info); err != nil {
		return err
	}
	defer os.Remove(infoPath)
	d.log.Printf("device %s, remote %q, listening on %s", d.device, d.repo.Remote, info.Addr)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	d.bgCtx = ctx
	go d.serve(ctx, ln)
	// Tracked, as the syncs it runs start background work of their own (clearOwnJunk).
	d.bg.Add(1)
	go func() {
		defer d.bg.Done()
		d.pollLoop(ctx)
	}()
	// The presence loop is drained like other background work: a push still in flight when
	// the goodbye deletes presence could otherwise land after it and bring the device back.
	d.bg.Add(1)
	go func() {
		defer d.bg.Done()
		d.presenceLoop(ctx)
	}()
	if opt.Idle > 0 {
		go d.idleLoop(ctx)
	}
	select {
	case <-ctx.Done():
	case <-d.quit:
	}
	cancel()
	ln.Close() // no new connections, so nothing joins bg while it drains
	drained := make(chan struct{})
	go func() { d.bg.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(20 * time.Second):
		d.log.Printf("background work still running at exit")
	}
	d.goodbye()
	return nil
}

func open(ctx context.Context, opt Options, dir string) (*Daemon, error) {
	now := time.Now()
	d := &Daemon{
		opt: opt, dir: dir, log: opt.Log, token: bus.RandHex(32), started: now, lastActive: now,
		sessions: map[string]*session{}, peers: map[string]*peer{}, known: map[string]string{},
		caughtUp: map[string]bool{}, unfetched: map[string]fetchFail{}, junkTried: map[string]time.Time{},
		kick: make(chan struct{}, 1), quit: make(chan struct{}),
	}
	device, err := loadDevice(dir)
	if err != nil {
		return nil, err
	}
	d.device = device
	d.host, _ = os.Hostname()
	d.repo = &bus.Repo{Git: gitx.Git{Dir: opt.GitDir}, Name: device, Email: device + "@" + brand.Name + ".invalid"}
	d.repo.Remote = d.repo.Config(ctx, brand.ConfigRemote)
	if d.repo.Remote == "" {
		d.repo.Remote = "origin"
	}
	if d.repo.Config(ctx, "remote."+d.repo.Remote+".url") == "" {
		return nil, fmt.Errorf("this clone has no remote %q; add one, or name the bus's remote with `git config %s <name>`",
			d.repo.Remote, brand.ConfigRemote)
	}
	d.owner = d.repo.Config(ctx, "user.name")
	if err := d.loadState(); err != nil {
		return nil, err
	}
	wall := now.Round(0)
	if !d.st.Initialized || wall.Sub(d.st.LastSync) > catchUpLimit {
		d.mode = modeSkip
	} else {
		d.mode = modeCatchUp
	}
	clearStaleLocks(opt.GitDir, d.log)
	if err := d.restore(ctx); err != nil {
		return nil, err
	}
	return d, nil
}

// restore rebuilds what the daemon knows from the local copies of bus refs, so it can answer
// before its first sync completes, or while the network is down.
func (d *Daemon) restore(ctx context.Context) error {
	local, err := d.repo.LocalRefs(ctx)
	if err != nil {
		return err
	}
	d.advMu.Lock()
	defer d.advMu.Unlock()
	for ref, sha := range local {
		d.known[ref] = sha
	}
	d.ownTip = local[d.meshRef()]
	if d.ownTip != "" {
		// Junk pushed over this device's log: build on the last commit read from it instead. On a
		// failure to tell, the same: a tip that is behind gets its push refused, and the next
		// try builds on the remote's.
		if ok, _ := d.repo.IsCommit(ctx, d.ownTip); !ok {
			d.ownTip = d.st.Processed[d.meshRef()]
			if ok, _ := d.repo.IsCommit(ctx, d.ownTip); d.ownTip != "" && !ok {
				d.ownTip = ""
			}
		}
	}
	for ref, sha := range local {
		if strings.HasPrefix(ref, brand.PresencePrefix) {
			d.updatePeer(ctx, ref, sha)
		}
	}
	return nil
}

func (d *Daemon) meshRef() string     { return brand.MeshPrefix + d.device }
func (d *Daemon) presenceRef() string { return brand.PresencePrefix + d.device }

// stop asks Run to return.
func (d *Daemon) stop() { d.quitOnce.Do(func() { close(d.quit) }) }

// goodbye deletes this device's presence, so other devices drop it at their next sync instead of
// finding it stale six minutes later.
func (d *Daemon) goodbye() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := d.repo.Push(ctx, "", d.presenceRef(), bus.FastForward, ""); err != nil {
		d.log.Printf("removing presence: %v", err)
	}
	d.log.Printf("stopped")
}

// clearStaleLocks removes .lock files under the bus refs. A git killed mid-write leaves one,
// and every later fetch of that ref fails until it is gone. The daemon holds the clone's lock
// and is the only writer of these refs, so one older than a few seconds is stale.
func clearStaleLocks(gitDir string, l *log.Logger) (removed bool) {
	root := filepath.Join(gitDir, filepath.FromSlash(brand.RefRoot))
	filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() || !strings.HasSuffix(path, ".lock") {
			return nil
		}
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > 30*time.Second {
			if os.Remove(path) == nil {
				l.Printf("removed stale lock %s", path)
				removed = true
			}
		}
		return nil
	})
	return removed
}

func (d *Daemon) idleLoop(ctx context.Context) {
	t := time.NewTicker(min(30*time.Second, d.opt.Idle/4+time.Millisecond))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		d.mu.Lock()
		idle := len(d.sessions) == 0 && time.Since(d.lastActive) > d.opt.Idle
		d.mu.Unlock()
		if idle {
			d.log.Printf("no sessions for %s; exiting", d.opt.Idle)
			d.stop()
			return
		}
	}
}

// loadDevice reads this clone's device ID, creating it on first run: the hostname plus four
// random hex digits, so two clones on one machine are two devices.
func loadDevice(dir string) (string, error) {
	path := filepath.Join(dir, "device")
	if b, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(b)); bus.ValidDevice(id) {
			return id, nil
		}
	}
	host, _ := os.Hostname()
	id := slug(host) + "-" + bus.RandHex(4)
	return id, os.WriteFile(path, []byte(id+"\n"), 0o600)
}

// slug makes a hostname safe as a ref component and short enough to read.
func slug(host string) string {
	host, _, _ = strings.Cut(strings.ToLower(host), ".")
	var b strings.Builder
	for _, r := range host {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 24 {
		s = strings.Trim(s[:24], "-")
	}
	if s == "" {
		s = "device"
	}
	return s
}

func validName(s string) bool { return bus.ValidName(s) }

func (d *Daemon) statePath() string { return filepath.Join(d.dir, "state.json") }

func (d *Daemon) loadState() error {
	d.st = state{Processed: map[string]string{}, Inbox: map[string][]*inMsg{}, Junked: map[string]time.Time{},
		Stopped: map[string]time.Time{}}
	b, err := os.ReadFile(d.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &d.st); err != nil {
		d.log.Printf("state.json unreadable, starting fresh: %v", err)
		d.st = state{}
	}
	if d.st.Processed == nil {
		d.st.Processed = map[string]string{}
	}
	if d.st.Junked == nil {
		d.st.Junked = map[string]time.Time{}
	}
	if d.st.Inbox == nil {
		d.st.Inbox = map[string][]*inMsg{}
	}
	if d.st.Stopped == nil {
		d.st.Stopped = map[string]time.Time{}
	}
	// V1's daemons also put notes about claims in inboxes, as from t4 itself, which no address
	// on the bus can be. This one writes none, and would show one as a teammate's message.
	for sess, box := range d.st.Inbox {
		if box = slices.DeleteFunc(box, func(m *inMsg) bool { return m.Event.From == brand.Name }); len(box) == 0 {
			delete(d.st.Inbox, sess)
		} else {
			d.st.Inbox[sess] = box
		}
	}
	return nil
}

// saveLocked writes state.json. Caller holds mu.
func (d *Daemon) saveLocked() {
	if err := writeJSON(d.statePath(), d.st); err != nil {
		d.log.Printf("saving state: %v", err)
	}
}

// writeJSON writes a file atomically, so a crash never leaves half of one.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// noteLocked adds a line to the recent-events log that `log` shows. Caller holds mu.
func (d *Daemon) noteLocked(at time.Time, line string) {
	d.recent = append(d.recent, proto.EventView{At: at, Line: line})
	if len(d.recent) > 500 {
		d.recent = d.recent[len(d.recent)-500:]
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
