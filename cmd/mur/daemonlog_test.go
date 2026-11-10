package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/daemon"
	"github.com/a-Fig/murmur/internal/join"
)

// What the daemon logs goes to its log through logfile's Writer, which takes credentials out of
// URLs (and keeps the file bounded, which internal/logfile tests).
func TestTheDaemonsLogHoldsNoCredentials(t *testing.T) {
	_, clone := cloneOf(t, t.TempDir())
	ctx := context.Background()
	p, err := join.Locate(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.RecordConsent(); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "daemon.log")
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
	err = runDaemonWith(ctx, []string{"-C", clone, "--log", logPath},
		func(_ context.Context, o daemon.Options) error {
			o.Log.Printf("fetching: git ls-remote https://x-access-token:SECRET@github.com/o/r: failed")
			log.Printf("from the standard logger: https://SECRET@github.com/o/r")
			return nil
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); strings.Contains(got, "SECRET") || strings.Count(got, "https://github.com/o/r") != 2 {
		t.Fatalf("the daemon's log holds:\n%s", got)
	}
}
