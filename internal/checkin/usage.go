// Package checkin is the daily check-in, the one thing Murmur sends anywhere but the team's own
// remote (#148). It is how active users are counted: everything else travels through each team's
// repo, so nothing else reaches the project. While a joined clone's daemon runs, at most once a UTC
// day, it sends murmur.afig.dev:
//
//   - a random ID for this machine's user account, the same in every clone it has;
//   - a one-way hash of the repo's remote, so users can be grouped into teams;
//   - mur's version, OS and architecture.
//
// No names, messages, branches or paths. The answer is the latest release, which `mur version`
// reports when it is newer. DO_NOT_TRACK or MURMUR_NO_USAGE in the environment, or
// `git config --global murmur.usage false`, turns it off; a development build never checks in.
package checkin

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/gitx"
)

// Options are a check-in's inputs; the zero values of the optional ones are the real ones.
type Options struct {
	Version string // mur's own; a development build ("-dev") never checks in
	GitDir  string // the clone's git common dir: its remote, its git config, and its state dir

	URL       string              // where to check in; brand.CheckInURL
	ConfigDir string              // holds the ID; the user's config dir, then "murmur"
	Getenv    func(string) string // os.Getenv
	Client    *http.Client        // 10 s timeout
	Now       func() time.Time    // time.Now
	Every     time.Duration       // how often to look whether today's check-in is due; 1 h
	Log       *log.Logger         // where a failed check-in is noted; nowhere
	OS, Arch  string              // runtime.GOOS and GOARCH
}

func (o *Options) defaults() error {
	if o.URL == "" {
		o.URL = brand.CheckInURL
	}
	if o.ConfigDir == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		o.ConfigDir = filepath.Join(dir, brand.Name)
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 10 * time.Second}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Every == 0 {
		o.Every = time.Hour
	}
	if o.Log == nil {
		o.Log = log.New(io.Discard, "", 0)
	}
	if o.OS == "" {
		o.OS, o.Arch = runtime.GOOS, runtime.GOARCH
	}
	return nil
}

// Off says whether the user turned check-ins off, and how, for the log.
func Off(ctx context.Context, getenv func(string) string, gitDir string) (bool, string) {
	for _, env := range []string{"DO_NOT_TRACK", brand.NoUsageEnv} {
		if v := strings.TrimSpace(getenv(env)); v != "" && v != "0" && !strings.EqualFold(v, "false") {
			return true, env + " is set"
		}
	}
	out, err := gitx.Git{Dir: gitDir}.Run(ctx, "", "config", "--type=bool", "--get", brand.ConfigUsage)
	if err == nil && strings.TrimSpace(out) == "false" {
		return true, "git config " + brand.ConfigUsage + " is false"
	}
	return false, ""
}

// Run checks in now if today's check-in is due, then looks again every Every until ctx ends. A
// daemon runs it for as long as it runs; a check-in that fails is tried again at the next look.
func Run(ctx context.Context, o Options) {
	if err := o.defaults(); err != nil {
		o.Log.Printf("no check-in: %v", err)
		return
	}
	if strings.HasSuffix(o.Version, "-dev") {
		return
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := checkInIfDue(ctx, o); err != nil && ctx.Err() == nil {
			o.Log.Printf("checking in: %v", err)
		}
		t.Reset(o.Every)
	}
}

// state is what the last check-in left in the clone's state dir.
type state struct {
	Day    string `json:"day"`    // its UTC day, YYYY-MM-DD
	Latest string `json:"latest"` // the latest release it was told of, without the v
}

func statePath(gitDir string) string { return filepath.Join(gitDir, brand.StateDir, "checkin.json") }

func readState(gitDir string) state {
	var st state
	if b, err := os.ReadFile(statePath(gitDir)); err == nil {
		json.Unmarshal(b, &st)
	}
	return st
}

// Request is what a check-in sends; site/src/worker.js reads it.
type Request struct {
	User    string `json:"user"`
	Team    string `json:"team"`
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func checkInIfDue(ctx context.Context, o Options) error {
	if off, _ := Off(ctx, o.Getenv, o.GitDir); off {
		return nil
	}
	day := o.Now().UTC().Format(time.DateOnly)
	st := readState(o.GitDir)
	if st.Day == day {
		return nil
	}
	user, err := userID(o.ConfigDir)
	if err != nil {
		return err
	}
	team, err := teamID(ctx, o.GitDir)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(Request{User: user, Team: team, Version: o.Version, OS: o.OS, Arch: o.Arch})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", brand.Command+"/"+o.Version)
	res, err := o.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", o.URL, res.Status)
	}
	var ans struct {
		Latest string `json:"latest"`
	}
	json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&ans)
	st = state{Day: day, Latest: strings.TrimPrefix(ans.Latest, "v")}
	if st.Latest == "" {
		st.Latest = readState(o.GitDir).Latest
	}
	b, _ := json.Marshal(st)
	if err := os.MkdirAll(filepath.Dir(statePath(o.GitDir)), 0o700); err != nil {
		return err
	}
	tmp := statePath(o.GitDir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(o.GitDir))
}

// userID is this user account's ID, made the first time: 16 random bytes, in hex. Two daemons
// making it at once both end up with the one that was written first.
func userID(dir string) (string, error) {
	path := filepath.Join(dir, "id")
	if b, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(b)); validHex(id, 32) {
			return id, nil
		}
		os.Remove(path) // not one this package wrote; replace it
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	buf := make([]byte, 16)
	rand.Read(buf)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		b, err := os.ReadFile(path)
		if id := strings.TrimSpace(string(b)); err == nil && validHex(id, 32) {
			return id, nil
		}
		return "", fmt.Errorf("unreadable %s", path)
	}
	if err != nil {
		return "", err
	}
	id := hex.EncodeToString(buf)
	_, err = f.WriteString(id + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return id, err
}

func validHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// teamID is a hash of the address of the remote the bus lives on, the same in every clone of the
// repo, however it was cloned: https or ssh, with or without .git or a user name.
func teamID(ctx context.Context, gitDir string) (string, error) {
	g := gitx.Git{Dir: gitDir}
	name, err := bus.RemoteFor(ctx, g)
	if err != nil {
		return "", err
	}
	url := (&bus.Repo{Git: g}).Config(ctx, "remote."+name+".url")
	sum := sha256.Sum256([]byte(brand.Name + " team\x00" + RepoKey(url)))
	return hex.EncodeToString(sum[:16]), nil
}

// RepoKey is a remote's address reduced to host and path, lowercased, so every way of writing one
// repo's address gives the same key: https://user@GitHub.com/a-Fig/murmur.git and
// git@github.com:a-Fig/murmur both give github.com/a-fig/murmur.
func RepoKey(url string) string {
	s := strings.TrimSpace(url)
	scheme := false
	if i := strings.Index(s, "://"); i >= 0 {
		s, scheme = s[i+3:], true
	}
	// Credentials or an ssh user end at the last @ before the path.
	slash := strings.IndexByte(s, '/')
	if at := strings.LastIndexByte(s, '@'); at >= 0 && (slash < 0 || at < slash) {
		s = s[at+1:]
	}
	if !scheme {
		// scp-like host:path, but not a Windows drive (C:\...)
		if i := strings.IndexByte(s, ':'); i > 1 && (slash < 0 || i < strings.IndexByte(s, '/')) {
			s = s[:i] + "/" + strings.TrimPrefix(s[i+1:], "/")
		}
	} else if host, rest, ok := strings.Cut(s, "/"); ok {
		if h, port, ok := strings.Cut(host, ":"); ok {
			if _, err := strconv.Atoi(port); err == nil {
				host = h
			}
		}
		s = host + "/" + rest
	}
	s = strings.TrimRight(strings.ReplaceAll(s, "\\", "/"), "/")
	s = strings.TrimSuffix(s, ".git")
	return strings.ToLower(s)
}

// Newer returns the latest release the clone's last check-in was told of, if it is newer than
// version, else "".
func Newer(gitDir, version string) string {
	latest := readState(gitDir).Latest
	if latest != "" && compare(latest, version) > 0 {
		return latest
	}
	return ""
}

// compare orders versions like 1.2.3 and 1.2.3-pre.1, as semver does for the ones mur's releases
// use: a pre-release comes before its release, and a development build ("-dev") counts as its
// release's pre-release.
func compare(a, b string) int {
	ac, ap, _ := strings.Cut(a, "-")
	bc, bp, _ := strings.Cut(b, "-")
	as, bs := strings.Split(ac, "."), strings.Split(bc, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case ap == bp:
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	}
	return strings.Compare(ap, bp)
}
