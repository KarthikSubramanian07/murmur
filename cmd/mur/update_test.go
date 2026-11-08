package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"testing"
	"time"

	"github.com/a-Fig/murmur/internal/daemon"
	"github.com/a-Fig/murmur/internal/join"
)

func TestUpdateRestartForwardsDaemonFlags(t *testing.T) {
	_, clone := cloneOf(t, t.TempDir())
	ctx := context.Background()
	p, err := join.Locate(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.RecordConsent(); err != nil {
		t.Fatal(err)
	}
	// The daemon can be started outside the clone. A relative log path must
	// continue to name the same file when its replacement starts in the clone.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	logPath, err := filepath.Rel(cwd, filepath.Join(t.TempDir(), "daemon custom.log"))
	if err != nil {
		t.Fatal(err)
	}
	absoluteLog, err := filepath.Abs(logPath)
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	logWriter := log.Writer()
	defer func() {
		if os.Stderr != stderr {
			os.Stderr.Close()
		}
		os.Stdout, os.Stderr = stdout, stderr
		log.SetOutput(logWriter)
		debug.SetCrashOutput(nil, debug.CrashOptions{})
	}()
	var gotFlags []string
	started := false
	err = runDaemonWith(ctx, []string{"-C", clone, "--poll", "17s", "--idle=-1s", "--log", logPath},
		func(_ context.Context, o daemon.Options) error {
			if o.Poll != 17*time.Second || o.Idle != -time.Second {
				t.Fatalf("options: poll=%s idle=%s", o.Poll, o.Idle)
			}
			return daemon.ErrUpdated
		}, func(exe, gitDir string, flags ...string) error {
			started = true
			if exe == "" || gitDir != filepath.Join(clone, ".git") {
				t.Fatalf("replacement target: %q %q", exe, gitDir)
			}
			gotFlags = flags
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--C=" + clone, "--idle=-1s", "--log=" + absoluteLog, "--poll=17s"}
	if !started || !reflect.DeepEqual(gotFlags, want) {
		t.Fatalf("started=%v flags=%q, want %q", started, gotFlags, want)
	}
}
