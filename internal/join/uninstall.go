package join

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/client"
	"github.com/a-Fig/murmur/internal/clones"
	"github.com/a-Fig/murmur/internal/jsonedit"
	"github.com/a-Fig/murmur/internal/setup"
	"github.com/a-Fig/murmur/internal/skills"
)

// Uninstall takes Murmur off this machine for this user (#399): what join writes once per user, in
// every agent configuration join registers in, the daemons still running, and the record of this
// machine's clones. A leave can't do that part, since the server entry serves every repo the user
// joined, and a leave can't tell whether its repo is the last. Uninstall can: it reads the record,
// and refuses while a clone on it is still joined, unless leave is set, when it leaves each first.
//
// It never touches a repo's tracked files: the section in AGENTS.md and CLAUDE.md is the team's. Nor
// the binary, which on Windows can't be deleted while it runs: the report says how.
func Uninstall(ctx context.Context, e Env, leave bool) (Report, error) {
	var r Report
	problem := func(what string, err error) { r.Problems = append(r.Problems, what+": "+err.Error()) }
	record, err := clones.DefaultDir()
	if err != nil {
		return r, fmt.Errorf("finding the record of this machine's clones: %w", err)
	}
	joined, err := clones.List(record, Joined)
	if err != nil {
		return r, fmt.Errorf("reading the record of this machine's clones (%s): %w", record, err)
	}
	if len(joined) > 0 && !leave {
		var b strings.Builder
		fmt.Fprintf(&b, "%s is still joined in %d %s on this machine:\n", brand.Product, len(joined), plural(len(joined), "clone", "clones"))
		for _, c := range joined {
			fmt.Fprintf(&b, "  - %s (%s)\n", c.Label(), c.Top())
		}
		fmt.Fprintf(&b, "Leave each with `%[1]s join --leave` in it, or run `%[1]s uninstall --leave` to leave them all and uninstall", brand.Command)
		return r, errors.New(b.String())
	}

	// Each clone is left first, so that nothing still joined is left with no server to serve it.
	// A clone whose leave didn't take its consent away stops the uninstall there.
	left := map[string]bool{}
	for _, c := range joined {
		lr, err := Leave(ctx, e, c.Top())
		for _, d := range lr.Did {
			r.Did = append(r.Did, c.Label()+": "+d)
		}
		r.Notes = append(r.Notes, lr.Notes...)
		for _, p := range lr.Problems {
			r.Problems = append(r.Problems, c.Label()+": "+p)
		}
		if err != nil && len(lr.Problems) == 0 {
			problem("leaving "+c.Top(), err)
		}
		if Joined(c.GitDir) {
			return r, fmt.Errorf("%s is still joined in %s, so nothing else was removed; see above", brand.Product, c.Top())
		}
		left[clones.ID(c.GitDir)] = true
	}

	// Per user: every agent configuration join registers in.
	byAgent, notes := e.homes()
	r.Notes = append(r.Notes, notes...)
	for i, a := range agents {
		for _, hs := range sameDir(byAgent[i]) {
			did, notes, err := a.unregister(e, ctx, hs)
			r.Did, r.Notes = append(r.Did, did...), append(r.Notes, notes...)
			if err != nil {
				problem(hs[0].Label, err)
			}
			if dir := skillsDir(a, hs[0]); dir != "" {
				did, notes, err := removeSkills(dir, hs[0].Label)
				r.Did, r.Notes = append(r.Did, did...), append(r.Notes, notes...)
				if err != nil {
					problem(hs[0].Label+" skills", err)
				}
			}
		}
	}

	// The daemons of clones that left before, by hand or by an older mur, and still run. A daemon
	// whose user left stops itself within a second; one from before 0.3 can't be asked (#389), and
	// Stop says so with its process ID.
	all, err := clones.List(record, func(string) bool { return true })
	if err != nil {
		problem("reading the record of this machine's clones", err)
	}
	for _, c := range all {
		if left[c.ID()] {
			continue
		}
		sctx, cancel := context.WithTimeout(ctx, time.Minute)
		stopped, err := client.Stop(sctx, c.GitDir)
		cancel()
		switch {
		case err != nil:
			problem("the daemon of "+c.Top(), err)
		case stopped:
			r.Did = append(r.Did, "Stopped the daemon of "+c.Top())
		}
	}

	if removed, err := removeRecord(record); err != nil {
		problem("removing the record of this machine's clones ("+record+")", err)
	} else if removed {
		r.Did = append(r.Did, "Removed the record of this machine's clones ("+record+")")
	}

	if len(r.Did) == 0 && len(r.Problems) == 0 {
		r.Did = []string{"Nothing to remove: " + brand.Product + " wasn't set up for any agent here"}
	}
	r.Left = e.leftovers()
	if len(r.Problems) > 0 {
		return r, errors.New("not everything was removed; see above. Running it again retries")
	}
	return r, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// sameDir groups homes by their configuration: two homes in one directory are one file to edit.
// Codex's can be (a home named by CODEX_HOME that is also the default, see sameCodexKey), and its
// hook's trust is then under both spellings.
func sameDir(hs []agentHome) [][]agentHome {
	var out [][]agentHome
	for _, h := range hs {
		if h.Dir == "" {
			continue
		}
		i := slices.IndexFunc(out, func(g []agentHome) bool { return samePath(g[0].Dir, h.Dir) && samePath(g[0].JSON, h.JSON) })
		if i < 0 {
			out = append(out, []agentHome{h})
		} else {
			out[i] = append(out[i], h)
		}
	}
	return out
}

// leftovers are what an uninstall leaves for the user, and how to remove each.
func (e Env) leftovers() []string {
	var out []string
	if e.Exe != "" {
		del := "rm '" + strings.ReplaceAll(e.Exe, "'", `'\''`) + "'"
		if runtime.GOOS == "windows" {
			del = "Remove-Item -LiteralPath " + psQuote(e.Exe)
		}
		out = append(out, "The "+brand.Command+" binary, which is running this command and so wasn't deleted: once it has exited, `"+del+"`. "+
			"Its folder ("+filepath.Dir(e.Exe)+") stays on PATH, as other programs may be installed there.")
	}
	return append(out,
		"In each clone that joined, the daemon's folder (.git/"+brand.StateDir+") and the bus's local refs (refs/"+brand.Name+"/), which nothing reads any more. "+
			"`git for-each-ref --format='delete %(refname)' refs/"+brand.Name+"/ | git update-ref --stdin` removes the refs.",
		"The "+brand.Product+" section in a repo's AGENTS.md and CLAUDE.md, which is the team's: it stays, and asks an agent there whether to join again.",
		"Agent sessions already open keep "+brand.Product+"'s server running until they end, and it does nothing; new ones don't start it.")
}

// removeRecord deletes the record of this machine's clones, the files clones writes, and the folder
// once it is empty, with the folder above it when that is the default one, Murmur's, and empty too.
func removeRecord(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, en := range entries {
		name := en.Name()
		if !en.Type().IsRegular() || !(strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".clone-")) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	if err := os.Remove(dir); err != nil {
		return false, err
	}
	if os.Getenv(clones.DirEnv) == "" {
		os.Remove(filepath.Dir(dir)) // fails, and stays, while it holds anything else (the sessions' records)
	}
	return true, nil
}

// readConfig is a user config file's text for an edit, "" for one that isn't there: a regular file
// or a link to one, read where writeAtomic writes, up to jsonedit's cap.
func readConfig(path string) (string, error) {
	b, err := jsonedit.ReadText(resolveLinks(path))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%s %w", path, err)
	}
	return string(b), nil
}

// writeJSONConfig writes an edited JSON or JSONC config once it parses: the editors keep every other
// byte, and a slip there would leave a file the agent can't load at all.
func writeJSONConfig(path, text string, perm os.FileMode) error {
	if err := parses(text); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := writeAtomic(path, []byte(text), perm); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// jsonSteps runs edits over text in turn, and reports whether any changed it.
func jsonSteps(text string, steps ...func(string) (string, bool, error)) (string, bool, error) {
	changed := false
	for _, s := range steps {
		var did bool
		var err error
		if text, did, err = s(text); err != nil {
			return "", false, err
		}
		changed = changed || did
	}
	return text, changed, nil
}

func deleteStep(path ...string) func(string) (string, bool, error) {
	return func(t string) (string, bool, error) { return jsonedit.DeleteIn(t, path) }
}

func emptyStep(path ...string) func(string) (string, bool, error) {
	return func(t string) (string, bool, error) { return deleteIfEmpty(t, path) }
}

// unregisterClaudeAt takes Murmur's server out of Claude Code home h, and its allow rule and any
// notice hook of Murmur's out of the settings there. The server goes through `claude mcp remove`
// where the command is there, as join adds it through `claude mcp add`: Claude Code rewrites
// .claude.json while it runs.
func (e Env) unregisterClaudeAt(ctx context.Context, hs []agentHome) (did, notes []string, err error) {
	h := hs[0]
	text, err := readConfig(h.JSON)
	if err != nil {
		return nil, nil, err
	}
	if text != "" {
		if _, found, err := jsonedit.GetIn(text, []string{"mcpServers", brand.MCPServer}); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", h.JSON, err)
		} else if found {
			claude, lerr := e.look("claude")
			if lerr != nil && h.Bin != "" {
				claude, lerr = h.Bin, nil
			}
			if lerr == nil {
				if out, err := e.runClaude(ctx, h, claude, "mcp", "remove", "--scope", "user", brand.MCPServer); err != nil {
					return nil, nil, fmt.Errorf("`claude mcp remove` failed: %v: %s", err, strings.TrimSpace(out))
				}
			} else {
				out, _, err := jsonedit.DeleteIn(text, []string{"mcpServers", brand.MCPServer})
				if err == nil {
					err = writeJSONConfig(h.JSON, out, 0o600)
				}
				if err != nil {
					return nil, nil, err
				}
			}
			did = append(did, h.Label+": removed the "+brand.MCPServer+" MCP server ("+h.JSON+")")
		}
	}

	path := filepath.Join(h.Dir, "settings.json")
	text, err = readConfig(path)
	if err != nil || text == "" {
		return did, nil, err
	}
	out, allowed, err := jsonedit.RemoveIn(text, []string{"permissions", "allow"}, allowRule)
	if err != nil {
		return did, nil, fmt.Errorf("%s: %w", path, err)
	}
	// A notice hook here is none of join's (it writes the hook per working tree), but one a user
	// copied from a worktree's file runs `mur hook`, which won't be there.
	hooked := false
	if cfg, perr := jsonedit.Parse([]byte(out)); perr == nil && setup.RemoveHooks(cfg) {
		b, err := jsonedit.Format(cfg, []byte(out))
		if err != nil {
			return did, nil, fmt.Errorf("%s: %w", path, err)
		}
		out, hooked = string(b), true
	}
	if !allowed && !hooked {
		return did, nil, nil
	}
	if err := writeJSONConfig(path, out, 0o644); err != nil {
		return did, nil, err
	}
	if allowed {
		did = append(did, h.Label+": removed the rule that allowed "+brand.Product+"'s tools ("+path+")")
	}
	if hooked {
		did = append(did, h.Label+": removed "+brand.Product+"'s notice hook ("+path+")")
	}
	return did, nil, nil
}

// unregisterCodexAt takes Murmur's server, hook and the hook's trust out of a Codex home. The hook
// goes first: Codex keys a hook's trust by its group's place in its event's list, so taking
// Murmur's group out moves the user's own groups after it up one, and their trust entries are moved
// with them, as join keeps its group in place for the same reason (editCodexHooks). If the trust
// can't be written after the hook went, the user's moved hooks ask once more in Codex, which is
// all a stale key costs.
func (e Env) unregisterCodexAt(_ context.Context, hs []agentHome) (did, notes []string, err error) {
	h := hs[0]
	hooksPath := filepath.Join(h.Dir, "hooks.json")
	removed, err := removeCodexHooks(hooksPath)
	if err != nil {
		return nil, nil, err
	}
	if len(removed) > 0 {
		did = append(did, h.Label+": removed "+brand.Product+"'s hook ("+hooksPath+")")
	}
	var prefixes []string
	for _, x := range hs {
		if p := codexKeyPath(hooksPath, x.Named) + ":"; !slices.Contains(prefixes, p) {
			prefixes = append(prefixes, p)
		}
	}
	path := filepath.Join(h.Dir, "config.toml")
	text, err := readConfig(path)
	if err != nil || text == "" {
		return did, nil, err
	}
	server, state := []string{"mcp_servers", brand.MCPServer}, []string{"hooks", "state"}
	targets := [][]string{server}
	if len(removed) > 0 {
		targets = append(targets, append(state, "*"))
	}
	out, gone, err := editTOMLTables(text, "config.toml", targets, func(p []string) ([]string, bool) {
		if hasPrefix(p, server) {
			return nil, true
		}
		if len(p) == 3 && hasPrefix(p, state) {
			key, drop := shiftTrust(p[2], prefixes, removed)
			if drop {
				return nil, true
			}
			if key != p[2] {
				return []string{"hooks", "state", key}, false
			}
		}
		return nil, false
	})
	if err != nil {
		return did, nil, fmt.Errorf("%s: %w", path, err)
	}
	if out == text {
		return did, nil, nil
	}
	if err := writeAtomic(path, []byte(out), 0o600); err != nil {
		return did, nil, fmt.Errorf("%s: %w", path, err)
	}
	if slices.ContainsFunc(gone, func(p []string) bool { return hasPrefix(p, server) }) {
		did = append(did, h.Label+": removed the "+brand.MCPServer+" MCP server ("+path+")")
	}
	if slices.ContainsFunc(gone, func(p []string) bool { return hasPrefix(p, state) }) {
		did = append(did, h.Label+": removed the trust of "+brand.Product+"'s hook ("+path+")")
	}
	return did, nil, nil
}

// removeCodexHooks takes every group of Murmur's (isMurmurGroup) out of the hooks.json at path, an
// event left with none taken out too, and returns the places they had, by event as Codex names it
// in trust keys (post_tool_use).
func removeCodexHooks(path string) (map[string][]int, error) {
	text, err := readConfig(path)
	if err != nil || text == "" {
		return nil, err
	}
	doc, err := jsonedit.Parse([]byte(text))
	if err != nil {
		return nil, fmt.Errorf("%s isn't a JSON object (%v), so it was left alone", path, err)
	}
	hooks := doc.Object("hooks")
	removed := map[string][]int{}
	for _, ev := range hooks.Keys() {
		v, _ := hooks.Get(ev)
		groups, ok := v.([]any)
		if !ok {
			continue
		}
		var kept []any
		for i, g := range groups {
			if isMurmurGroup(g) {
				removed[snakeCase(ev)] = append(removed[snakeCase(ev)], i)
			} else {
				kept = append(kept, g)
			}
		}
		switch {
		case len(kept) == len(groups):
		case len(kept) == 0:
			hooks.Delete(ev)
		default:
			hooks.Set(ev, kept)
		}
	}
	if len(removed) == 0 {
		return nil, nil
	}
	out, err := jsonedit.Format(doc, []byte(text))
	if err == nil {
		err = writeAtomic(path, out, 0o600)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return removed, nil
}

// snakeCase is an event's name as Codex writes it in a trust key: PostToolUse is post_tool_use.
func snakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// shiftTrust is what becomes of the trust entry keyed key once the groups removed are out of
// hooks.json: drop for one of theirs, and the key with its group's new place for a later group of
// the same event. A key under none of prefixes (another file's hooks) stays as it is.
func shiftTrust(key string, prefixes []string, removed map[string][]int) (string, bool) {
	for _, p := range prefixes {
		rest, ok := strings.CutPrefix(key, p)
		if !ok {
			continue
		}
		parts := strings.Split(rest, ":")
		if len(parts) != 3 {
			return key, false
		}
		at, err := strconv.Atoi(parts[1])
		gone := removed[parts[0]]
		if err != nil || len(gone) == 0 {
			return key, false
		}
		if slices.Contains(gone, at) {
			return "", true
		}
		before := 0
		for _, g := range gone {
			if g < at {
				before++
			}
		}
		parts[1] = strconv.Itoa(at - before)
		return p + strings.Join(parts, ":"), false
	}
	return key, false
}

// editTOMLTables returns text with each table header passed to act, which says what becomes of the
// table: drop takes it out, its header and its lines, with the blank line before it; a non-nil
// rename gives the header that path. act sees the headers of subtables too ([mcp_servers.murmur.env]),
// and must have no effects: it is also asked about keys. Every other byte stays. dropped are the
// headers of the tables taken out.
//
// targets are the tables act may change, "*" in one standing for any key: a key that sets one act
// would change from outside its own table (a dotted key, or an inline table that holds it) is
// refused, as join refuses it, since this edit would miss it.
func editTOMLTables(text, file string, targets [][]string, act func(path []string) (rename []string, drop bool)) (out string, dropped [][]string, err error) {
	lines := strings.Split(text, "\n")
	type header struct {
		at   int
		drop bool
	}
	var headers []header
	open := ""
	var table []string
	dropping, renamed := false, false
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		inString := open != ""
		if open = tomlLineEnd(line, open); inString {
			continue
		}
		if path, ok := tableHeader(line); ok {
			table = path
			rename, d := act(path)
			dropping = d
			headers = append(headers, header{i, d})
			if d {
				dropped = append(dropped, path)
			} else if rename != nil {
				name := make([]string, len(rename))
				for j, k := range rename {
					name[j] = tomlBareKey(k)
				}
				lb, rb := "[", "]"
				if strings.HasPrefix(strings.TrimSpace(line), "[[") {
					lb, rb = "[[", "]]"
				}
				lines[i] = lb + strings.Join(name, ".") + rb + raw[len(line):]
				renamed = true
			}
			continue
		}
		if dropping {
			continue
		}
		key, _, ok := keyValue(line)
		if !ok {
			continue
		}
		full := append(append([]string(nil), table...), key...)
		for _, t := range targets {
			touched := false
			if len(full) >= len(t) && matchesPrefix(full, t) && !matchesPrefix(table, t) {
				rename, d := act(full[:len(t)])
				touched = d || rename != nil
			}
			if touched || prefixOf(full, t) {
				return "", nil, fmt.Errorf("%s sets %s in a form uninstall doesn't edit (a dotted key or an inline table): take %s's entries out of it by hand", file, strings.Join(full, "."), brand.Product)
			}
		}
	}
	if len(dropped) == 0 && !renamed {
		return text, nil, nil
	}
	// A dropped table goes from its header to its last line with anything on it, and the blank line
	// before it goes too, so the tables around it keep one blank line between them. One with nothing
	// kept before it takes its trailing blank lines instead. The empty string after the last newline
	// is the file's end, not a line, and stays.
	drop := make([]bool, len(lines))
	for n, h := range headers {
		if !h.drop {
			continue
		}
		next := len(lines)
		if n+1 < len(headers) {
			next = headers[n+1].at
		}
		last := h.at
		for i := h.at; i < next; i++ {
			if strings.TrimSpace(lines[i]) != "" {
				last = i
			}
		}
		for i := h.at; i <= last; i++ {
			drop[i] = true
		}
		keptBefore := false
		for i := 0; i < h.at; i++ {
			if !drop[i] && strings.TrimSpace(lines[i]) != "" {
				keptBefore = true
				break
			}
		}
		switch {
		case !keptBefore:
			for i := last + 1; i < next && i < len(lines)-1; i++ {
				drop[i] = true
			}
			for i := 0; i < h.at; i++ {
				drop[i] = true // blank lines only
			}
		case h.at > 0 && strings.TrimSpace(lines[h.at-1]) == "":
			drop[h.at-1] = true
		}
	}
	var kept []string
	for i, l := range lines {
		if !drop[i] {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n"), dropped, nil
}

// tomlBareKey writes a key bare where TOML allows it, else quoted (tomlKey).
func tomlBareKey(k string) string {
	if k != "" && strings.Trim(k, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") == "" {
		return k
	}
	return tomlKey(k)
}

// matchesPrefix says path starts with pattern, "*" in pattern matching any key.
func matchesPrefix(path, pattern []string) bool {
	if len(path) < len(pattern) {
		return false
	}
	for i, p := range pattern {
		if p != "*" && p != path[i] {
			return false
		}
	}
	return true
}

// prefixOf says path is a strict prefix of pattern: a key there sets the whole of what pattern names.
func prefixOf(path, pattern []string) bool {
	return len(path) < len(pattern) && matchesPrefix(pattern[:len(path)], path)
}

// unregisterGrokAt takes Murmur's server, its allow rule and its hook file out of a Grok home.
func (e Env) unregisterGrokAt(_ context.Context, hs []agentHome) (did, notes []string, err error) {
	h := hs[0]
	path := filepath.Join(h.Dir, "config.toml")
	text, err := readConfig(path)
	if err != nil {
		return nil, nil, err
	}
	if text != "" {
		server := []string{"mcp_servers", brand.MCPServer}
		out, gone, err := editTOMLTables(text, "config.toml", [][]string{server}, func(p []string) ([]string, bool) {
			return nil, hasPrefix(p, server)
		})
		dropped := len(gone) > 0
		var allowed bool
		if err == nil {
			out, allowed, err = removeTOMLListItem(out, "permission", "allow", grokAllow, "config.toml")
		}
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		if out != text {
			if err := writeAtomic(path, []byte(out), 0o600); err != nil {
				return nil, nil, fmt.Errorf("%s: %w", path, err)
			}
		}
		if dropped {
			did = append(did, h.Label+": removed the "+brand.MCPServer+" MCP server ("+path+")")
		}
		if allowed {
			did = append(did, h.Label+": removed the rule that allowed "+brand.Product+"'s tools ("+path+")")
		}
	}
	hook := filepath.Join(h.Dir, "hooks", grokHookFile)
	gone, note, err := removeOwned(hook, func(b []byte) bool { return bytes.Contains(b, []byte(" hook grok")) })
	if gone {
		did = append(did, h.Label+": removed "+brand.Product+"'s hook ("+hook+")")
	}
	if note != "" {
		notes = append(notes, h.Label+": "+note)
	}
	return did, notes, err
}

// removeTOMLListItem is addTOMLListItem's undoing: text without item in the list key of [table].
// changed is false, and out is text, when it isn't there. A list in a form join doesn't edit is
// left, and is an error only if it holds item.
func removeTOMLListItem(text, table, key, item, file string) (out string, changed bool, err error) {
	lines := strings.Split(text, "\n")
	open := ""
	var current []string
	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		inString := open != ""
		if open = tomlLineEnd(line, open); inString {
			continue
		}
		if path, ok := tableHeader(line); ok {
			current = path
			continue
		}
		path, val, ok := keyValue(line)
		if !ok || !slicesEqual(current, []string{table}) || len(path) != 1 || path[0] != key {
			continue
		}
		items, after, ok := stringListRest(val)
		if !singleLine(val) || !ok || (after != "" && after[0] != '#') {
			if strings.Contains(val, item) {
				return "", false, fmt.Errorf("%s has %s in [%s] in a form uninstall doesn't edit: take %q out of it by hand", file, key, table, item)
			}
			return text, false, nil
		}
		if !slices.Contains(items, item) {
			return text, false, nil
		}
		items = slices.DeleteFunc(items, func(s string) bool { return s == item })
		if after != "" {
			after = " " + after
		}
		lines[i] = key + " = [" + joinQuoted(items) + "]" + after + raw[len(line):]
		return strings.Join(lines, "\n"), true, nil
	}
	return text, false, nil
}

// removeOwned deletes the file at path if ours says it is one Murmur wrote. A file that isn't, or
// that is a link (the link is the user's), is left, with a note saying so.
func removeOwned(path string, ours func([]byte) bool) (removed bool, note string, err error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if !fi.Mode().IsRegular() {
		return false, "left " + path + " alone: it isn't a regular file (a link, say), so it may be someone else's; delete it if it is " + brand.Product + "'s", nil
	}
	b, err := jsonedit.ReadText(path)
	if err != nil {
		return false, "", fmt.Errorf("%s %w", path, err)
	}
	if !ours(b) {
		return false, "left " + path + " alone: it isn't the one " + brand.Product + " writes", nil
	}
	if err := os.Remove(path); err != nil {
		return false, "", err
	}
	return true, "", nil
}

// unregisterOpenCodeAt takes Murmur's entries, of either shape, out of both of OpenCode's files.
func (e Env) unregisterOpenCodeAt(_ context.Context, hs []agentHome) (did, notes []string, err error) {
	h := hs[0]
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		path := filepath.Join(h.Dir, name)
		text, err := readConfig(path)
		if err != nil {
			return did, notes, err
		}
		if text == "" {
			continue
		}
		out, server, err := jsonSteps(text,
			deleteStep(openCodeV2.server...), emptyStep("mcp", "servers"),
			deleteStep(openCodeV1.server...), emptyStep("mcp"))
		if err != nil {
			return did, notes, fmt.Errorf("%s: %w", path, err)
		}
		out, rule, err := jsonSteps(out,
			func(t string) (string, bool, error) {
				return jsonedit.RemoveIn(t, []string{"permissions"}, openCodeV2Rule)
			},
			emptyStep("permissions"),
			deleteStep(openCodeV1Rule...), emptyStep("permission"))
		if err != nil {
			return did, notes, fmt.Errorf("%s: %w", path, err)
		}
		if out == text {
			continue
		}
		if err := writeJSONConfig(path, out, 0o644); err != nil {
			return did, notes, err
		}
		if server {
			did = append(did, h.Label+": removed the "+brand.MCPServer+" MCP server ("+path+")")
		}
		if rule {
			did = append(did, h.Label+": removed the rule that allowed "+brand.Product+"'s tools ("+path+")")
		}
	}
	return did, notes, nil
}

// unregisterMuseAt takes Murmur's server out of Muse's settings.json.
func (e Env) unregisterMuseAt(_ context.Context, hs []agentHome) (did, notes []string, err error) {
	path := filepath.Join(hs[0].Dir, "settings.json")
	text, err := readConfig(path)
	if err != nil || text == "" {
		return nil, nil, err
	}
	out, changed, err := jsonSteps(text, deleteStep("mcpServers", brand.MCPServer), emptyStep("mcpServers"))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if !changed {
		return nil, nil, nil
	}
	if err := writeJSONConfig(path, out, 0o644); err != nil {
		return nil, nil, err
	}
	return []string{hs[0].Label + ": removed the " + brand.MCPServer + " MCP server (" + path + ")"}, nil, nil
}

// unregisterPiAt deletes Murmur's Pi extension, which is both its server and its hook there.
func (e Env) unregisterPiAt(_ context.Context, hs []agentHome) (did, notes []string, err error) {
	path := piExtensionPath(hs[0].Dir)
	gone, note, err := removeOwned(path, func(b []byte) bool { return bytes.HasPrefix(b, []byte(piMarker)) })
	if gone {
		did = append(did, hs[0].Label+": removed "+brand.Product+"'s extension, its MCP server and hook there ("+path+")")
	}
	if note != "" {
		notes = append(notes, hs[0].Label+": "+note)
	}
	return did, notes, err
}

// removeSkills deletes Murmur's skills from an agent's skills folder: each SKILL.md with the
// skills' marker, and its folder once that is empty. One without the marker is a user's own.
func removeSkills(dir, label string) (did, notes []string, err error) {
	var gone []string
	for _, s := range skills.All() {
		path := filepath.Join(dir, s.Name, "SKILL.md")
		removed, note, err := removeOwned(path, func(b []byte) bool { return bytes.Contains(b, []byte(skills.Marker)) })
		if note != "" {
			notes = append(notes, label+": "+note)
		}
		if err != nil {
			return did, notes, err
		}
		if removed {
			os.Remove(filepath.Dir(path)) // stays if anything else is in it
			gone = append(gone, s.Name)
		}
	}
	if len(gone) > 0 {
		did = append(did, label+": removed the "+strings.Join(gone, " and ")+" skills ("+dir+")")
	}
	return did, notes, nil
}
