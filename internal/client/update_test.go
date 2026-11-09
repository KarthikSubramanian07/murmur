package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
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

// The detached child exits after recording its argv. No daemon or network runs.
func TestMain(m *testing.M) {
	if path := os.Getenv("MURMUR_TEST_DAEMON_ARGS"); path != "" && len(os.Args) > 1 && os.Args[1] == "daemon" {
		b, err := json.Marshal(os.Args[1:])
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
	if err := StartDaemon(exe, gitDir, flags...); err != nil {
		t.Fatal(err)
	}
	want := append(append([]string{"daemon"}, flags...), "--git-dir", gitDir)
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		b, err := os.ReadFile(captured)
		var got []string
		if err != nil || json.Unmarshal(b, &got) != nil {
			continue
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
