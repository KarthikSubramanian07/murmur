// Package web is the page where a human sees every session working in the repo, the messages
// between them, and this device's settings. The daemon serves it on loopback; the page reaches
// the daemon only through Source.
//
// Any website the user visits can send requests to localhost, so every API request must carry
// the daemon's token, and every request must name the server by its loopback address, which
// defeats DNS rebinding. No CORS headers are sent, so no other origin can read a response.
package web

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
)

//go:embed static
var static embed.FS

var index = template.Must(template.ParseFS(static, "static/index.html"))

const (
	maxRequest    = 64 << 10
	sourceTimeout = 10 * time.Second
	maxNameLen    = 64
	maxOverrides  = 1000
	maxCursor     = 256
)

// contentPolicy lets the page load only its own assets and talk only to its own server.
const contentPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; " +
	"img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Server serves the page and its API.
type Server struct {
	src   Source
	token string
	prs   *prCache
	mux   *http.ServeMux
	page  []byte
}

// New returns the page's handler. token is the daemon's; repoDir is the clone, where gh runs to
// look up pull requests. The handler must be served on 127.0.0.1.
func New(src Source, token, repoDir string) *Server {
	return NewWithPRs(src, token, GH(repoDir))
}

// NewWithPRs is New with another way to look up pull requests, such as the dev server's
// fixtures.
func NewWithPRs(src Source, token string, prs LookupPR) *Server {
	s := &Server{src: src, token: token, prs: newPRCache(prs), mux: http.NewServeMux()}
	var page bytes.Buffer
	if err := index.Execute(&page, struct{ Name string }{brand.Product}); err != nil {
		panic(err) // the template is embedded, so this is a bug, not a condition
	}
	s.page = page.Bytes()

	assets, err := fs.Sub(static, "static")
	if err != nil {
		panic(err)
	}
	files := http.FileServerFS(assets)
	s.mux.HandleFunc("GET /{$}", s.serveIndex)
	s.mux.Handle("GET /assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r) // no directory listings
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	}))
	s.mux.HandleFunc("GET /api/state", s.getState)
	s.mux.HandleFunc("GET /api/messages", s.getMessages)
	s.mux.HandleFunc("GET /api/settings", s.getSettings)
	s.mux.HandleFunc("PUT /api/settings", s.putSettings)
	s.mux.HandleFunc("POST /api/unmute", s.postUnmute)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !hostOK(r) {
		http.Error(w, "unexpected Host", http.StatusMisdirectedRequest)
		return
	}
	h := w.Header()
	h.Set("Content-Security-Policy", contentPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		h.Set("Cache-Control", "no-store")
		if !s.authorized(r) {
			writeError(w, http.StatusUnauthorized, "missing or wrong token")
			return
		}
		if r.Header.Get(hiddenHeader) != "" {
			r = r.WithContext(context.WithValue(r.Context(), hiddenKey{}, true))
		}
	}
	s.mux.ServeHTTP(w, r)
}

// hiddenHeader marks a call from a page nobody can see: a background tab, a minimized window, a
// locked screen. Such a page goes on polling, slowly, so that it is current when it is looked at
// again, and a tab forgotten for days would otherwise look like someone watching.
const hiddenHeader = "X-Page-Hidden"

type hiddenKey struct{}

// Watched says whether the call ctx belongs to came from a page someone can see. The Source's
// methods get that ctx, so a daemon can let only a watched page keep it running.
func Watched(ctx context.Context) bool {
	hidden, _ := ctx.Value(hiddenKey{}).(bool)
	return !hidden
}

// hostOK accepts only a Host naming the loopback address and port the request arrived on. A
// page on another site that rebinds its own name to 127.0.0.1 still sends its own name here.
func hostOK(r *http.Request) bool {
	addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return false
	}
	_, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return false
	}
	host := strings.ToLower(r.Host)
	return host == "127.0.0.1:"+port || host == "localhost:"+port
}

func (s *Server) authorized(r *http.Request) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && s.token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(s.page)
}

// stateResponse is State with each branch's open pull request, by branch name. Branches with
// none, or not looked up yet, are left out.
type stateResponse struct {
	State
	PRs map[string]PR `json:"prs"`
}

func (s *Server) getState(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), sourceTimeout)
	defer cancel()
	st, err := s.src.State(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if st.Devices == nil {
		st.Devices = []Device{}
	}
	if st.Mutes == nil {
		st.Mutes = []Mute{}
	}
	var branches []string
	for i, d := range st.Devices {
		if d.Sessions == nil {
			st.Devices[i].Sessions = []Session{}
		}
		for _, ss := range d.Sessions {
			branches = append(branches, ss.Branch)
		}
	}
	writeJSON(w, stateResponse{State: st, PRs: s.prs.get(branches)})
}

type messagesResponse struct {
	Messages []Message `json:"messages"`
	Cursor   string    `json:"cursor"`
}

func (s *Server) getMessages(w http.ResponseWriter, r *http.Request) {
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > maxCursor {
		writeError(w, http.StatusBadRequest, "cursor too long")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), sourceTimeout)
	defer cancel()
	msgs, next, err := s.src.Messages(ctx, cursor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if msgs == nil {
		msgs = []Message{}
	}
	writeJSON(w, messagesResponse{Messages: msgs, Cursor: next})
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), sourceTimeout)
	defer cancel()
	set, err := s.src.Settings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, set)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var set Settings
	if err := readJSON(w, r, &set); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := CheckSettings(set); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), sourceTimeout)
	defer cancel()
	saved, err := s.src.SetSettings(ctx, set)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, saved)
}

// CheckSettings says whether settings are ones the page may set. The API makes this check before
// calling the source; a source that also reads settings from elsewhere, a file say, makes it too.
func CheckSettings(set Settings) error {
	if !set.Wake.valid() {
		return errors.New("wake: when must be always, within or never, and minutes 0 to 1440, given only with within")
	}
	if len(set.SessionWake) > maxOverrides {
		return errors.New("too many session overrides")
	}
	for name, w := range set.SessionWake {
		if name == "" || len(name) > maxNameLen {
			return errors.New("sessionWake: bad session name")
		}
		if !w.valid() {
			return errors.New("sessionWake " + name + ": when must be always, within or never, and minutes 0 to 1440, given only with within")
		}
	}
	if set.Busy != "" && !validBusy(set.Busy) {
		return errors.New("busy: must be subject or whole")
	}
	if len(set.SessionBusy) > maxOverrides {
		return errors.New("too many session overrides")
	}
	for name, b := range set.SessionBusy {
		if name == "" || len(name) > maxNameLen {
			return errors.New("sessionBusy: bad session name")
		}
		if !validBusy(b) {
			return errors.New("sessionBusy " + name + ": must be subject or whole")
		}
	}
	return nil
}

type unmuteRequest struct {
	By    string `json:"by"`
	Muted string `json:"muted"`
}

func (s *Server) postUnmute(w http.ResponseWriter, r *http.Request) {
	var u unmuteRequest
	if err := readJSON(w, r, &u); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if u.By == "" || u.Muted == "" || len(u.By) > maxNameLen || len(u.Muted) > maxNameLen {
		writeError(w, http.StatusBadRequest, "by and muted must be session names")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), sourceTimeout)
	defer cancel()
	if err := s.src.Unmute(ctx, u.By, u.Muted); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequest))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.New("bad JSON: " + err.Error())
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{msg})
}
