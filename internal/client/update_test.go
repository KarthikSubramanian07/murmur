package client

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/proc"
)

func TestUpdatedMCPStartsInstalledBinary(t *testing.T) {
	for _, c := range []struct{ input, want string }{
		{"mur.exe.old-.mur-update-123", "mur.exe"},
		{"mur (deleted)", "mur"},
		{"mur.exe", "mur.exe"},
		{filepath.Join("dir.old-.mur-update-name", "mur.exe"), filepath.Join("dir.old-.mur-update-name", "mur.exe")},
	} {
		if got := installedExecutable(c.input); got != c.want {
			t.Errorf("installedExecutable(%q)=%q, want %q", c.input, got, c.want)
		}
	}
}

// started is what the detached child records: its argv, and the address of the page's socket it
// inherited, if it was handed one, or why it couldn't use it.
type started struct {
	Args []string
	Page string
}

// The detached child exits after recording what it was started with. No daemon or network runs.
func TestMain(m *testing.M) {
	if path := os.Getenv("MURMUR_TEST_DAEMON_ARGS"); path != "" && len(os.Args) > 1 && os.Args[1] == "daemon" {
		rec := started{Args: os.Args[1:]}
		if f := proc.Inherited(brand.PageListenerEnv, "page"); f != nil {
			if ln, err := net.FileListener(f); err != nil {
				rec.Page = "error: " + err.Error()
			} else {
				rec.Page = ln.Addr().String()
			}
		}
		b, err := json.Marshal(rec)
		if err == nil {
			err = os.WriteFile(path, b, 0600)
		}
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestStartDaemonForwardsFlags(t *testing.T) {
	gitDir := stateOnly(t)
	captured := filepath.Join(t.TempDir(), "args.json")
	t.Setenv("MURMUR_TEST_DAEMON_ARGS", captured)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	customLog := filepath.Join(t.TempDir(), "custom daemon.log")
	flags := []string{"--poll=17s", "--idle=-1s", "--log=" + customLog}
	if err := StartDaemon(exe, gitDir, nil, flags...); err != nil {
		t.Fatal(err)
	}
	want := append(append([]string{"daemon"}, flags...), "--git-dir", gitDir)
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		b, err := os.ReadFile(captured)
		var rec started
		if err != nil || json.Unmarshal(b, &rec) != nil {
			continue
		}
		got := rec.Args
		if rec.Page != "" {
			t.Fatalf("a daemon started without the page's socket was told of one: %q", rec.Page)
		}
		// The Windows WMI fallback additionally passes the inherited log's name.
		if len(got) == len(want)+2 && got[len(want)] == "--log" && got[len(want)+1] == customLog {
			got = got[:len(want)]
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("child argv=%q, want %q", got, want)
		}
		if _, err := os.Stat(customLog); err != nil {
			t.Fatalf("custom log was not used: %v", err)
		}
		return
	}
	t.Fatal("detached child did not record its arguments")
}

// A daemon restarting onto an update hands the page's socket to the new one (#255): the child
// inherits it, and is told its descriptor, on each OS.
func TestStartDaemonHandsOverThePage(t *testing.T) {
	gitDir := stateOnly(t)
	captured := filepath.Join(t.TempDir(), "args.json")
	t.Setenv("MURMUR_TEST_DAEMON_ARGS", captured)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	page, err := ln.(*net.TCPListener).File()
	ln.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	if err := StartDaemon(exe, gitDir, page); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		b, err := os.ReadFile(captured)
		var rec started
		if err != nil || json.Unmarshal(b, &rec) != nil {
			continue
		}
		// A start through WMI (Windows, in a job that forbids breakaway) can't hand anything over,
		// and then tells the child of nothing.
		wmi := len(rec.Args) >= 2 && rec.Args[len(rec.Args)-2] == "--log"
		if rec.Page != ln.Addr().String() && !(wmi && rec.Page == "") {
			t.Fatalf("the child's page socket: %q, want %s", rec.Page, ln.Addr())
		}
		return
	}
	t.Fatal("detached child did not record what it was started with")
}
