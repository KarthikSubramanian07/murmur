package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v1.2.4", "1.2.3", true}, {"1.10.0", "1.9.9", true}, {"2.0.0", "1.99.99", true},
		{"1.2.3", "1.2.3", false}, {"1.2.2", "1.2.3", false}, {"1.2.3", "1.2.3-rc.1", true},
		{"1.2.3-rc.10", "1.2.3-rc.2", true}, {"1.2.3-rc.1", "1.2.3", false},
		{"garbage", "1.2.3", false}, {"1.2", "1.2.3", false}, {"01.2.3", "1.2.3", false},
		{"1.2.3+build", "1.2.3", false}, {"1.2.3-", "1.2.3", false},
	} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q,%q)=%v", c.a, c.b, got)
		}
	}
}
func TestAssets(t *testing.T) {
	for _, osname := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			want := "mur-" + osname + "-" + arch
			if osname == "windows" {
				want += ".exe"
			}
			if got := assetName(osname, arch); got != want {
				t.Errorf("asset=%s", got)
			}
		}
	}
	if assetName("linux", "386") != "" || assetName("plan9", "amd64") != "" {
		t.Fatal("unsupported asset")
	}
}
func TestChecksums(t *testing.T) {
	data := []byte("new binary")
	sum := fmt.Sprintf("%x", sha256.Sum256(data))
	for _, marker := range []string{" ", "*"} {
		if err := verify(data, []byte(sum+" "+marker+"mur-linux-amd64\n"), "mur-linux-amd64"); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []string{strings.Repeat("0", 64) + "  mur-linux-amd64", sum + "  other", "", "junk  mur-linux-amd64"} {
		if verify(data, []byte(s), "mur-linux-amd64") == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
func TestReplace(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		for _, failAt := range []int{0, 1, 2} {
			if goos == "linux" && failAt == 2 {
				continue
			}
			t.Run(fmt.Sprintf("%s/fail%d", goos, failAt), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "mur")
				os.WriteFile(path, []byte("old"), 0755)
				calls := 0
				err := replace(path, []byte("new"), goos, func(a, b string) error {
					calls++
					if calls == failAt {
						return errors.New("injected rename failure")
					}
					return os.Rename(a, b)
				})
				want := "new"
				if failAt > 0 {
					want = "old"
					if err == nil {
						t.Fatal("no failure")
					}
				} else if err != nil {
					t.Fatal(err)
				}
				b, e := os.ReadFile(path)
				if e != nil || string(b) != want {
					t.Fatalf("binary %q: %v, want %s", b, e, want)
				}
				entries, _ := os.ReadDir(filepath.Dir(path))
				if len(entries) != 1 {
					t.Fatalf("leftovers: %v", entries)
				}
			})
		}
	}
}
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if b, e := exec.Command("git", "init", "-q", dir).CombinedOutput(); e != nil {
		t.Fatalf("%s: %v", b, e)
	}
	return filepath.Join(dir, ".git")
}
func TestOptOut(t *testing.T) {
	gitDir := repo(t)
	ctx := context.Background()
	for _, v := range []string{"1", "true", "yes"} {
		if !Off(ctx, func(string) string { return v }, gitDir) {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"", "0", "false", "FALSE"} {
		if Off(ctx, func(string) string { return v }, gitDir) {
			t.Fatal(v)
		}
	}
	getenv := func(k string) string {
		if k == "DO_NOT_TRACK" || k == "MURMUR_NO_USAGE" {
			return "1"
		}
		return ""
	}
	if Off(ctx, getenv, gitDir) {
		t.Fatal("usage opt-outs disabled updates")
	}
	if b, e := exec.Command("git", "--git-dir="+gitDir, "config", "murmur.update", "false").CombinedOutput(); e != nil {
		t.Fatalf("%s: %v", b, e)
	}
	if !Off(ctx, getenv, gitDir) {
		t.Fatal("git opt-out ignored")
	}
}
func TestReleases(t *testing.T) {
	for _, mode := range []string{"good", "bad", "missing", "same", "older", "prerelease", "dev", "off", "changed"} {
		t.Run(mode, func(t *testing.T) {
			var hits atomic.Int32
			data := []byte("new executable")
			var url string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				switch r.URL.Path {
				case "/latest":
					tag := "v1.2.4"
					if mode == "same" {
						tag = "v1.2.3"
					}
					if mode == "older" {
						tag = "v1.2.2"
					}
					json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "prerelease": mode == "prerelease", "assets": []map[string]string{
						{"name": "mur-linux-amd64", "browser_download_url": url + "/binary"}, {"name": "SHA256SUMS", "browser_download_url": url + "/sums"},
					}})
				case "/sums":
					sum := fmt.Sprintf("%x", sha256.Sum256(data))
					name := "mur-linux-amd64"
					if mode == "bad" {
						sum = strings.Repeat("0", 64)
					}
					if mode == "missing" {
						name = "other"
					}
					fmt.Fprintf(w, "%s  %s\n", sum, name)
				case "/binary":
					w.Write(data)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			url = srv.URL
			path := filepath.Join(t.TempDir(), "mur")
			os.WriteFile(path, []byte("old"), 0755)
			var logs bytes.Buffer
			o := Options{Version: "1.2.3", GitDir: repo(t), Executable: path, URL: url + "/latest", OS: "linux", Arch: "amd64", Client: srv.Client(), Getenv: func(string) string { return "" }, Log: log.New(&logs, "", 0), Every: time.Hour}
			if mode == "dev" {
				o.Version += "-dev"
			}
			if mode == "off" {
				o.Getenv = func(string) string { return "1" }
			}
			restarted := false
			if mode == "changed" {
				original, _ := identity(path)
				replacement := path + ".new"
				os.WriteFile(replacement, data, 0755)
				os.Rename(path, path+".old")
				os.Rename(replacement, path)
				done, err := attempt(context.Background(), o, original)
				if !done || err != nil || hits.Load() != 0 {
					t.Fatalf("changed: %v %v", done, err)
				}
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			Run(ctx, o, func() { restarted = true })
			b, _ := os.ReadFile(path)
			if mode == "good" {
				if !restarted || string(b) != string(data) || !strings.Contains(logs.String(), "1.2.3 -> 1.2.4") {
					t.Fatalf("update: restart=%v data=%s logs=%s", restarted, b, &logs)
				}
			} else if restarted || string(b) != "old" {
				t.Fatalf("unexpected install: %v %s", restarted, b)
			}
			if (mode == "dev" || mode == "off") && hits.Load() != 0 {
				t.Fatal("opted-out request")
			}
		})
	}
}
func TestCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mur.exe")
	for _, p := range []string{path, path + ".old-.mur-update-123", path + ".old-unrelated"} {
		os.WriteFile(p, nil, 0600)
	}
	cleanup(path)
	if _, err := os.Stat(path + ".old-.mur-update-123"); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".old-unrelated"); err != nil {
		t.Fatal(err)
	}
}

func TestFailedChecksArePeriodicAndQuiet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 3 {
			defer cancel()
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "mur")
	if err := os.WriteFile(path, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	Run(ctx, Options{Version: "1.2.3", GitDir: repo(t), Executable: path, URL: srv.URL, Client: srv.Client(), Every: 20 * time.Millisecond, Getenv: func(string) string { return "" }, Log: log.New(&logs, "", 0)}, func() { t.Error("restarted after failed check") })
	if hits.Load() < 2 {
		t.Fatalf("only %d checks", hits.Load())
	}
	if logs.Len() != 0 {
		t.Fatalf("network failure logged: %s", &logs)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "old" {
		t.Fatalf("binary changed: %s %v", b, err)
	}
}

func TestUnwritableInstallLogsOnceAndStops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request from unwritable install")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "mur")
	if err := os.WriteFile(path, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	lockPath := path + ".update.lock"
	if err := os.WriteFile(lockPath, nil, 0444); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(lockPath, 0600)
	// Some test environments run as root and can write despite the permission bits.
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0600)
	if err == nil {
		f.Close()
		t.Skip("permissions do not prevent writing")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	Run(ctx, Options{Version: "1.2.3", GitDir: repo(t), Executable: path, URL: srv.URL, Client: srv.Client(), Every: time.Millisecond, Getenv: func(string) string { return "" }, Log: log.New(&logs, "", 0)}, func() { t.Error("restarted") })
	if ctx.Err() != nil {
		t.Fatal("retried until context expired")
	}
	if strings.Count(logs.String(), "disabled until restart") != 1 {
		t.Fatalf("log: %s", &logs)
	}
}

func TestManualReinstallDuringDownload(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mur")
			if err := os.WriteFile(path, []byte("original"), 0755); err != nil {
				t.Fatal(err)
			}
			original, err := identity(path)
			if err != nil {
				t.Fatal(err)
			}
			data := []byte("release 1.2.4")
			manual := []byte("manual install 1.2.5")
			name := assetName(goos, "amd64")
			var url string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/latest":
					json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.2.4", "assets": []map[string]string{
						{"name": name, "browser_download_url": url + "/binary"}, {"name": "SHA256SUMS", "browser_download_url": url + "/sums"},
					}})
				case "/sums":
					fmt.Fprintf(w, "%x  %s\n", sha256.Sum256(data), name)
				case "/binary":
					// Complete a manual reinstall while the updater is waiting for the asset.
					// Neither install script takes the updater's lock.
					for _, err := range []error{os.WriteFile(path+".manual", manual, 0755), os.Rename(path, path+".old"), os.Rename(path+".manual", path)} {
						if err != nil {
							t.Error(err)
							http.Error(w, "manual reinstall failed", 500)
							return
						}
					}
					w.Write(data)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			url = srv.URL
			var logs bytes.Buffer
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done, err := attempt(ctx, Options{Version: "1.2.3", Executable: path, URL: url + "/latest", OS: goos, Arch: "amd64", Client: srv.Client(), Log: log.New(&logs, "", 0)}, original)
			if err != nil || !done {
				t.Fatalf("should restart onto manual install: done=%v err=%v", done, err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, manual) {
				t.Fatalf("manual install overwritten: %q (%v)", got, err)
			}
			if logs.Len() != 0 {
				t.Fatalf("claimed an update it did not install: %s", &logs)
			}
		})
	}
}
