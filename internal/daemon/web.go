package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/web"
)

// The page for humans (#22). The daemon serves internal/web on loopback and is its web.Source.
// Everything the page shows is what the daemon already holds in memory, so a poll costs no git
// call and no network, and no lock is held while one is made.

// defaultWakeMinutes is what a wake setting of 0 minutes stands for.
const defaultWakeMinutes = 3

// listenPage opens the page's port. It is never the sessions' port: those carry the daemon's
// protocol, and the page speaks HTTP to a browser. Only 127.0.0.1 will do, not just any loopback
// address: the page answers only requests addressed to 127.0.0.1 or localhost (web's Host check),
// so on [::1] or 127.0.0.2 it would refuse everything sent to the address it gave out.
func (d *Daemon) listenPage() (net.Listener, error) {
	host, _, err := net.SplitHostPort(d.opt.PageAddr)
	if err != nil {
		return nil, err
	}
	if host != "127.0.0.1" {
		return nil, fmt.Errorf("%s isn't on 127.0.0.1, the loopback address the page answers on", d.opt.PageAddr)
	}
	return net.Listen("tcp", d.opt.PageAddr)
}

// servePage serves the page on ln until ctx is done.
func (d *Daemon) servePage(ctx context.Context, ln net.Listener) {
	srv := &http.Server{
		Handler:           web.New(pageSource{d}, d.token, repoDir(d.opt.GitDir)),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          d.log,
	}
	d.bg.Add(2)
	go func() {
		defer d.bg.Done()
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) && ctx.Err() == nil {
			d.log.Printf("the page is no longer served: %v", err)
		}
	}()
	go func() {
		defer d.bg.Done()
		<-ctx.Done()
		// Let a request in flight finish: none waits on anything slow.
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if srv.Shutdown(sctx) != nil {
			srv.Close()
		}
	}()
}

// repoDir is the clone's top directory, where gh finds the repository from its remotes.
func repoDir(gitDir string) string {
	if filepath.Base(gitDir) == ".git" {
		return filepath.Dir(gitDir)
	}
	return gitDir
}

// pageSource is the daemon as the page sees it. The page's server calls it only for requests that
// carry the daemon's token, so each call is proof that a page is open; one from a page someone can
// see (web.Watched) keeps the daemon running.
type pageSource struct{ d *Daemon }

var _ web.Source = pageSource{}

// State lists this device and every device the daemon knows, with their sessions. A session has
// its name for an address; one without a name yet, or on a daemon that gives none, has the
// device/session address instead, which `send` takes just as well.
func (s pageSource) State(ctx context.Context) (web.State, error) {
	d := s.d
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	d.pageSeenLocked(ctx, now)

	self := web.Device{ID: d.device, Host: d.host, Owner: d.owner, Self: true, Reachable: true, LastSeen: now}
	live := d.liveNamesLocked()
	for _, ss := range d.sortedSessionsLocked() {
		if !ss.dropped.IsZero() && live[ss.name] {
			continue // its name went to the session that took over, which stands for the conversation
		}
		if d.offMeshLocked(ss.id) {
			continue // offline: its T3 thread is settled (#189)
		}
		addr := bus.Addr(d.device, ss.id)
		self.Sessions = append(self.Sessions, web.Session{
			Name: nameOr(ss.name, addr), Addr: addr, Agent: ss.agent, Branch: ss.branch,
			Committed: d.committedLocked(ss.id), Doing: ss.shownDoing(), Worktree: ss.worktree, Started: ss.started,
			LastActive: ss.lastActive})
	}
	st := web.State{Device: d.device, Devices: []web.Device{self}}
	for _, id := range sortedKeys(d.peers) {
		p := d.peers[id]
		dv := web.Device{ID: id, Host: p.pres.Host, Owner: p.pres.Owner, Reachable: !d.staleLocked(p),
			LastSeen: p.lastChange}
		for _, ps := range p.pres.Sessions {
			if ps.ID == proto.CLISession {
				continue // V1 daemons list their CLI
			}
			addr := bus.Addr(id, ps.ID)
			dv.Sessions = append(dv.Sessions, web.Session{
				Name: nameOr(ps.Name, addr), Addr: addr, Agent: ps.Agent, Branch: ps.Branch,
				Committed: ps.Committed, Doing: ps.Doing, Started: ps.Started, LastActive: peerActive(p, ps)})
		}
		st.Devices = append(st.Devices, dv)
	}
	return st, nil
}

func nameOr(name, addr string) string {
	if name != "" {
		return name
	}
	return addr
}

// pageMsg is a message the page can show: the event as the bus carried it, and its number. Names
// are put in when the page asks, so a name that lands later still shows on older messages.
type pageMsg struct {
	seq uint64
	e   bus.Event
}

// recordMessageLocked keeps a message for the page, dropping the oldest once there are
// PageMessages. Every message this daemon reads goes in, whoever it is for: anyone who can read
// the repo can read them all already. Caller holds mu.
func (d *Daemon) recordMessageLocked(e bus.Event) {
	if d.msgIDs[e.ID] {
		return
	}
	if len(d.msgs) >= d.opt.PageMessages {
		delete(d.msgIDs, d.msgs[0].e.ID)
		d.msgs = d.msgs[:copy(d.msgs, d.msgs[1:])]
	}
	d.msgSeq++
	d.msgs = append(d.msgs, pageMsg{d.msgSeq, e})
	d.msgIDs[e.ID] = true
}

// A cursor is this run of the daemon and the number of the last message returned. Numbers only go
// up, so a cursor still means "after this one" when the messages before it have been dropped. A
// cursor from another run, which a page could hold across a restart, has seen nothing.
func (d *Daemon) cursor(seq uint64) string {
	return strconv.FormatInt(d.started.UnixNano(), 36) + "-" + strconv.FormatUint(seq, 10)
}

func (d *Daemon) cursorSeq(cursor string) (uint64, error) {
	if cursor == "" {
		return 0, nil
	}
	run, num, ok := strings.Cut(cursor, "-")
	seq, err := strconv.ParseUint(num, 10, 64)
	if !ok || err != nil {
		return 0, errors.New("bad cursor")
	}
	if run != strconv.FormatInt(d.started.UnixNano(), 36) {
		return 0, nil
	}
	return seq, nil
}

// Messages returns the messages after cursor, oldest first as this daemon read them, which is not
// by the senders' clocks.
func (s pageSource) Messages(ctx context.Context, cursor string) ([]web.Message, string, error) {
	d := s.d
	after, err := d.cursorSeq(cursor)
	if err != nil {
		return nil, "", err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pageSeenLocked(ctx, time.Now())

	after = min(after, d.msgSeq)
	first := sort.Search(len(d.msgs), func(i int) bool { return d.msgs[i].seq > after })
	var byAddr map[string]string // the registry's names, for a session no longer in presence
	label := func(addr string) string {
		if name, _, _ := d.sessionInfoLocked(addr); name != "" {
			return name
		}
		if byAddr == nil {
			byAddr = d.namesByAddrLocked()
		}
		return nameOr(byAddr[addr], addr)
	}
	out := make([]web.Message, 0, len(d.msgs)-first)
	for _, m := range d.msgs[first:] {
		msg := web.Message{ID: m.e.ID, From: label(m.e.From), Subject: m.e.Subject, Body: m.e.Body, At: m.e.At,
			FromAddr: m.e.From, ToAddr: m.e.To}
		switch {
		case m.e.To == "*":
			msg.To, msg.ToKind = "*", web.ToAll
		case !strings.Contains(m.e.To, "/"):
			msg.To, msg.ToKind = m.e.To, web.ToDevice
		default:
			msg.To, msg.ToKind = label(m.e.To), web.ToSession
		}
		out = append(out, msg)
	}
	return out, d.cursor(d.msgSeq), nil
}

// pageSettings is what settings.json holds: the human's choices, and nothing the daemon works out.
type pageSettings struct {
	Wake        web.Wake            `json:"wake"`
	SessionWake map[string]web.Wake `json:"sessionWake,omitempty"`
	// Busy and SessionBusy are what a session at work gets from the hook (#204). A file from
	// before they existed has neither, which reads as the subject only.
	Busy        string            `json:"busy,omitempty"`
	SessionBusy map[string]string `json:"sessionBusy,omitempty"`
}

func (p pageSettings) view() web.Settings {
	return web.Settings{Wake: p.Wake, SessionWake: maps.Clone(p.SessionWake), Busy: p.Busy, SessionBusy: maps.Clone(p.SessionBusy),
		DefaultMinutes: defaultWakeMinutes}
}

// normalized is p with the empty maps dropped and the device default spelled out.
func (p pageSettings) normalized() pageSettings {
	if len(p.SessionWake) == 0 {
		p.SessionWake = nil
	}
	if len(p.SessionBusy) == 0 {
		p.SessionBusy = nil
	}
	if p.Busy == "" {
		p.Busy = web.BusySubject
	}
	return p
}

func (d *Daemon) settingsPath() string { return filepath.Join(d.dir, "settings.json") }

// loadSettings reads settings.json, or takes the defaults if there is none or it holds something
// the page wouldn't have saved. The file is left alone then: the next save replaces it.
func (d *Daemon) loadSettings() {
	d.settings = pageSettings{Wake: web.Wake{When: web.WakeWithin}, Busy: web.BusySubject}
	b, err := os.ReadFile(d.settingsPath())
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var read pageSettings
	if err == nil {
		if err = json.Unmarshal(b, &read); err == nil {
			err = web.CheckSettings(read.view())
		}
	}
	if err != nil {
		d.log.Printf("settings.json isn't usable, so the defaults stand: %v", err)
		return
	}
	d.settings = read.normalized()
}

func (s pageSource) Settings(ctx context.Context) (web.Settings, error) {
	s.d.pageCalled(ctx)
	s.d.setMu.Lock()
	defer s.d.setMu.Unlock()
	return s.d.settings.view(), nil
}

// SetSettings keeps the settings in memory only once they are on disk, so a failed save leaves
// the ones that were in force.
func (s pageSource) SetSettings(ctx context.Context, set web.Settings) (web.Settings, error) {
	d := s.d
	d.pageCalled(ctx)
	if err := web.CheckSettings(set); err != nil {
		return web.Settings{}, err
	}
	next := pageSettings{Wake: set.Wake, SessionWake: maps.Clone(set.SessionWake), Busy: set.Busy,
		SessionBusy: maps.Clone(set.SessionBusy)}.normalized()
	d.setMu.Lock()
	defer d.setMu.Unlock()
	if err := writeJSON(d.settingsPath(), next); err != nil {
		return web.Settings{}, fmt.Errorf("saving settings: %w", err)
	}
	d.settings = next
	d.kickPush() // a session that was set to never may be woken now, for what it has waiting
	return next.view(), nil
}

// Unmute is for mutes (#27), which don't exist yet.
func (s pageSource) Unmute(ctx context.Context, by, muted string) error {
	s.d.pageCalled(ctx)
	return errors.New("mutes aren't built yet (#27), so there is nothing to unmute")
}

func (d *Daemon) pageCalled(ctx context.Context) {
	d.mu.Lock()
	d.pageSeenLocked(ctx, time.Now())
	d.mu.Unlock()
}

// pageSeenLocked notes a call from the page, which keeps the daemon running only if someone can
// see the page: a tab left in the background goes on polling for as long as the browser runs.
// Caller holds mu.
func (d *Daemon) pageSeenLocked(ctx context.Context, now time.Time) {
	if web.Watched(ctx) {
		if !d.pageOpenLocked(now) {
			d.kickPoll() // a page opens: its view should not be 30 s behind
		}
		d.lastPage = now
	}
}

// pageOpenLocked says a page someone can see has called within PageOpen of now. Caller holds mu.
func (d *Daemon) pageOpenLocked(now time.Time) bool {
	return !d.lastPage.IsZero() && now.Sub(d.lastPage) <= d.opt.PageOpen
}
