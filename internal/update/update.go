// Package update installs checksum-verified GitHub releases, and keeps the latest release's version
// so `mur version` can say when there is a newer one. It talks only to GitHub, and not at all while
// updates are off. It takes stable releases only, unless the prerelease channel is chosen.
package update

import (
	"context"
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
	"strings"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/gitx"
	"github.com/a-Fig/murmur/internal/proc"
)

type Options struct {
	Version, GitDir string
	URL             string // the repo's releases API; overridden only by tests
	Executable      string // resolved running executable; tests use a disposable file
	// Running is what Identity said of the executable when the process started. A different file
	// at that path by the time Run starts is a newer install, not this process's binary to replace.
	// Left nil, Run takes the identity itself.
	Running  os.FileInfo
	OS, Arch string
	Client   *http.Client
	Getenv   func(string) string
	Log      *log.Logger
	Every    time.Duration
}

func Off(ctx context.Context, getenv func(string) string, gitDir string) bool {
	v := strings.TrimSpace(getenv("MURMUR_NO_UPDATE"))
	if v != "" && v != "0" && !strings.EqualFold(v, "false") {
		return true
	}
	out, err := (gitx.Git{Dir: gitDir}).Run(ctx, "", "config", "--type=bool", "--get", "murmur.update")
	return err == nil && strings.TrimSpace(out) == "false"
}

// Prerelease says whether git config chooses the prerelease channel (`murmur.channel prerelease`),
// which takes the newest published release, prerelease or stable. Any other value is the stable
// channel. It is a key of its own because daemons from before it read murmur.update as a bool, and
// they ignore this one, so they keep taking stable releases until one of those brings the channel.
func Prerelease(ctx context.Context, gitDir string) bool {
	out, err := (gitx.Git{Dir: gitDir}).Run(ctx, "", "config", "--get", "murmur.channel")
	return err == nil && strings.EqualFold(strings.TrimSpace(out), "prerelease")
}

// Run checks immediately and hourly. Network failures wait quietly until the next check.
// A successful install calls restart once; its caller waits for handoffs before stopping.
func Run(ctx context.Context, o Options, restart func()) {
	if strings.HasSuffix(o.Version, "-dev") {
		return
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Log == nil {
		o.Log = log.New(io.Discard, "", 0)
	}
	if o.URL == "" {
		o.URL = "https://api.github.com/repos/a-Fig/murmur/releases"
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 2 * time.Minute}
	}
	if o.OS == "" {
		o.OS, o.Arch = runtime.GOOS, runtime.GOARCH
	}
	if o.Every == 0 {
		o.Every = time.Hour
	}
	var err error
	if o.Executable == "" {
		o.Executable, err = os.Executable()
		if err != nil {
			return
		}
	}
	o.Executable, err = filepath.EvalSymlinks(o.Executable)
	if err != nil {
		return
	}
	original := o.Running
	if original == nil {
		if original, err = Identity(o.Executable); err != nil {
			return
		}
	}
	if o.OS == "windows" {
		cleanup(o.Executable)
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if !Off(ctx, o.Getenv, o.GitDir) {
			done, err := attempt(ctx, o, original)
			if done {
				restart()
				return
			}
			if errors.Is(err, os.ErrPermission) {
				o.Log.Printf("self-update disabled until restart: binary directory is not writable: %v", err)
				return
			}
		}
		timer.Reset(o.Every)
	}
}

func assetName(goos, arch string) string {
	if (goos != "linux" && goos != "darwin" && goos != "windows") || (arch != "amd64" && arch != "arm64") {
		return ""
	}
	name := "mur-" + goos + "-" + arch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func get(ctx context.Context, o Options, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mur/"+o.Version)
	res, err := o.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: %s", res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("download too large")
	}
	return b, err
}

func attempt(ctx context.Context, o Options, original os.FileInfo) (bool, error) {
	// Serialize daemons from different clones sharing this install. Never replace a binary
	// another updater has already installed with a release selected by this older process.
	lock, err := proc.Lock(o.Executable + ".update.lock")
	if err != nil {
		return false, err
	}
	defer lock.Close()
	current, err := Identity(o.Executable)
	if err != nil {
		return false, err
	}
	if !os.SameFile(original, current) {
		return true, nil
	}
	rel, err := pick(ctx, o, Prerelease(ctx, o.GitDir))
	if err != nil || rel == nil {
		return false, err
	}
	recordLatest(o.GitDir, rel.Tag)
	if !newer(rel.Tag, o.Version) {
		return false, nil
	}
	name := assetName(o.OS, o.Arch)
	if name == "" {
		return false, nil
	}
	urls := map[string]string{}
	for _, a := range rel.Assets {
		urls[a.Name] = a.URL
	}
	if urls[name] == "" || urls["SHA256SUMS"] == "" {
		return false, errors.New("missing release assets")
	}
	sums, err := get(ctx, o, urls["SHA256SUMS"], 1<<20)
	if err != nil {
		return false, err
	}
	data, err := get(ctx, o, urls[name], 128<<20)
	if err != nil {
		return false, err
	}
	if err = verify(data, sums, name); err != nil {
		return false, err
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	// Manual installers do not take our lock. Do not overwrite a reinstall that
	// completed while the release was downloading; restart onto it instead.
	current, err = Identity(o.Executable)
	if err != nil {
		return false, err
	}
	if !os.SameFile(original, current) {
		return true, nil
	}
	if err = replace(o.Executable, data, o.OS, os.Rename); err != nil {
		return false, err
	}
	o.Log.Printf("self-update: %s -> %s", o.Version, strings.TrimPrefix(rel.Tag, "v"))
	return true, nil
}

type release struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// pick returns the release to compare against the running version, or nil if there is none. The
// stable channel asks for GitHub's latest release, which is never a prerelease. The prerelease
// channel lists every release and takes the highest version, not the newest date, so a stable
// release published after a higher prerelease doesn't pull the channel back. It reads every page,
// since an old candidate can outrank the newest releases; old nightlies are pruned, so that is
// usually one request. A failed page, or more than ten, fails the check rather than choosing from
// part of the list.
func pick(ctx context.Context, o Options, prerelease bool) (*release, error) {
	if !prerelease {
		b, err := get(ctx, o, o.URL+"/latest", 1<<20)
		if err != nil {
			return nil, err
		}
		var r release
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		if r.Draft || r.Prerelease {
			return nil, nil
		}
		return &r, nil
	}
	const perPage, maxPages = 100, 10
	var best *release
	for page := 1; ; page++ {
		if page > maxPages {
			return nil, errors.New("more releases than the prerelease channel reads")
		}
		b, err := get(ctx, o, fmt.Sprintf("%s?per_page=%d&page=%d", o.URL, perPage, page), 16<<20)
		if err != nil {
			return nil, err
		}
		var all []release
		if err = json.Unmarshal(b, &all); err != nil {
			return nil, err
		}
		for i := range all {
			r := &all[i]
			if r.Draft {
				continue
			}
			if _, _, ok := version(r.Tag); ok && (best == nil || newer(r.Tag, best.Tag)) {
				best = r
			}
		}
		if len(all) < perPage {
			return best, nil
		}
	}
}

func verify(data, sums []byte, name string) error {
	sum := sha256.Sum256(data)
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			if strings.EqualFold(fields[0], hex.EncodeToString(sum[:])) {
				return nil
			}
			return errors.New("release checksum mismatch")
		}
	}
	return errors.New("release checksum entry missing")
}

// replace stages beside the destination, closes and syncs before renaming. The Windows
// backup is unique: a still-running old MCP server may hold an earlier backup open.
func replace(path string, data []byte, goos string, rename func(string, string) error) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".mur-update-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Chmod(st.Mode().Perm())
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if goos != "windows" {
		return rename(tmp, path)
	}
	old := path + ".old-" + filepath.Base(tmp)
	if err = rename(path, old); err != nil {
		return err
	}
	if err = rename(tmp, path); err != nil {
		if rollback := rename(old, path); rollback != nil {
			return fmt.Errorf("install failed: %v; restoring %s failed: %w", err, old, rollback)
		}
		return err
	}
	os.Remove(old) // fails harmlessly while the old exe is running
	return nil
}

func cleanup(path string) {
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), filepath.Base(path)+".old-.mur-update-") {
			os.Remove(filepath.Join(filepath.Dir(path), e.Name()))
		}
	}
}

// File.Stat captures Windows file IDs now; os.Stat can defer loading the ID
// until SameFile, by which time the path may already name the replacement.
func Identity(path string) (os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

// latest is the latest release last seen on the clone's channel, kept in its state dir.
type latest struct {
	Latest string `json:"latest"` // its version, without the v
}

func latestPath(gitDir string) string { return filepath.Join(gitDir, brand.StateDir, "latest.json") }

func recordLatest(gitDir, tag string) {
	if gitDir == "" {
		return
	}
	if _, _, ok := version(tag); !ok {
		return
	}
	b, _ := json.Marshal(latest{Latest: strings.TrimPrefix(tag, "v")})
	path := latestPath(gitDir)
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		os.Rename(tmp, path)
	}
}

// Newer returns the latest release the clone's daemon last saw, if it is newer than version, else "".
func Newer(gitDir, version string) string {
	var l latest
	if b, err := os.ReadFile(latestPath(gitDir)); err == nil {
		json.Unmarshal(b, &l)
	}
	if l.Latest != "" && newer(l.Latest, version) {
		return l.Latest
	}
	return ""
}
