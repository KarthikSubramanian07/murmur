package join

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/a-Fig/murmur/internal/setup"
)

// hookEvents reads which events a settings file runs a mur hook on, with the matcher of each.
func hookEvents(t *testing.T, path string) map[string]string {
	t.Helper()
	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(read(t, path)), &cfg); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for event, groups := range cfg.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				if !setup.IsHookCommand(h.Command) {
					continue
				}
				if _, dup := out[event]; dup {
					t.Errorf("%s has the hook twice", event)
				}
				out[event] = g.Matcher
			}
		}
	}
	return out
}

// wantHookEvents is what the hook must be installed on, written out so that a change to
// setup.HookEvents has to change this too. PreToolUse and PostToolUseFailure are how the daemon
// finds the commits a tool call made (#21); a join that leaves them out loses those commits.
func wantHookEvents() map[string]string {
	return map[string]string{"PreToolUse": "*", "PostToolUse": "*", "PostToolUseFailure": "*", "UserPromptSubmit": "", "Stop": ""}
}

// A join over an install from before the hook watched tool calls (PostToolUse and UserPromptSubmit
// only) adds the events it lacks, and doesn't double the ones it has or touch the user's own hooks.
func TestJoinCompletesAnOlderHookInstall(t *testing.T) {
	p, _ := clone(t)
	exe := filepath.Join(t.TempDir(), "bin", "mur")
	command, err := json.Marshal(setup.HookCommand(exe))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.Top, ".claude", "settings.local.json")
	write(t, path, `{
  "hooks": {
    "PostToolUse": [
      {"matcher": "Edit", "hooks": [{"type": "command", "command": "other-tool"}]},
      {"matcher": "*", "hooks": [{"type": "command", "command": `+string(command)+`}]}
    ],
    "UserPromptSubmit": [{"hooks": [{"type": "command", "command": `+string(command)+`}]}]
  }
}
`)
	changed, err := installHooks(context.Background(), p, exe)
	if err != nil || len(changed) == 0 {
		t.Fatalf("changed %q, err %v", changed, err)
	}
	if events := hookEvents(t, path); !reflect.DeepEqual(events, wantHookEvents()) {
		t.Errorf("the hook is on %v, want %v", events, wantHookEvents())
	}
	if got := read(t, path); strings.Count(got, "other-tool") != 1 {
		t.Errorf("the user's own hook was changed:\n%s", got)
	}
	if again, err := installHooks(context.Background(), p, exe); err != nil || len(again) != 0 {
		t.Errorf("a second install changed %q (err %v)", again, err)
	}
}
