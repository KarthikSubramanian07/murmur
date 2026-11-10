package join

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/a-Fig/murmur/internal/brand"
)

// Grok's hook (#225). Grok runs every file in hooks/ in its home for every project and never asks
// about them, where a project's own hooks wait for the folder to be trusted (Grok Build's hooks
// docs, read 2026-11-08). Join writes its own file there, which it owns whole. Grok takes Claude
// Code's payload (with the session ID as sessionId) and Claude Code's answers.
//
// Two events, the fewest that do the work: PostToolUse brings the notices, and Stop ends the turn
// and keeps it going with what wasn't read (#202). Not UserPromptSubmit, which Codex has: Grok
// discards what that hook prints, so notices handed to it would be marked delivered and lost. No
// matcher: Grok's is a regular expression, and none matches every tool.
//
// Grok wasn't installed on any machine this was written on, so none of it has been seen run.

// grokHookFile is the name of join's file in Grok's hooks/.
var grokHookFile = brand.MCPServer + ".json"

// grokHooks is the content of that file, running command.
func grokHooks(command string) ([]byte, error) {
	handler := []map[string]any{{"type": "command", "command": command, "timeout": codexHookTimeout}}
	doc := map[string]any{"hooks": map[string]any{
		"PostToolUse": []map[string]any{{"hooks": handler}},
		"Stop":        []map[string]any{{"hooks": handler}},
	}}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// installGrokHook writes join's hook file in Grok home h, if it isn't there as it would write it.
func (e Env) installGrokHook(h agentHome) (did []string, err error) {
	path := filepath.Join(h.Dir, "hooks", grokHookFile)
	want, err := grokHooks(agentHookCommand(e.Exe, "grok"))
	if err != nil {
		return nil, err
	}
	if have, err := os.ReadFile(path); err == nil && bytes.Equal(have, want) {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := writeAtomic(path, want, 0o600); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return []string{h.Label + ": added " + brand.Product + "'s hook, in every project (" + path + ")"}, nil
}

// fileExists says path is there and isn't a directory.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}
