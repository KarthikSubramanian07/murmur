package join

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/a-Fig/murmur/internal/brand"
)

// OpenCode 2 has no hooks of Claude Code's kind, so join writes a plugin of Murmur's own into its
// config directory's plugins/, which OpenCode loads in its server with no entry in opencode.json
// (#431, #205). It brings a session its messages as Murmur's hook does in Claude Code (#202): it
// runs `mur hook --agent opencode` at the same moments, with Claude Code's event names and
// OpenCode's session ID, which is the ID the shared MCP server registers the session by
// (internal/mcpserver, sharedAgent). Seen on OpenCode 2.2.0, 2026-11-12, with `opencode run
// --standalone` against a scratch config (the PR that added this says what was run):
//
//   - ctx.tool.hook("execute.after") fires after each tool call and is PostToolUse. What the hook
//     gives goes in with ctx.session.synthetic({sessionID, text, delivery: "steer"}): OpenCode puts
//     it after the tool's result and before the next model request, in the same execution, as a
//     message of its own that is kept in the session. Appended to the tool's result instead, a
//     model took it for text planted in the file it had read, and refused it (#431).
//   - ctx.event.subscribe({}) gives every event of the server, each with data.sessionID.
//     session.execution.started is UserPromptSubmit: the session is at work. So is every turn the
//     plugin itself starts.
//   - session.execution.succeeded is Stop. What the hook gives back goes in with
//     ctx.session.synthetic, which starts another execution, so the session goes on with it.
//     session.execution.interrupted (Escape, ctx.session.interrupt) is a Stop that was interrupted,
//     as Pi's is: the daemon takes the session for idle and hands over what is unread as a turn.
//     Any other session.execution.* event is taken the same way, a failed one among them, whose
//     name wasn't seen.
//   - session.created carries parentID for a session the `subagent` tool started (called `task`
//     before 2.2), and ctx.session.get gives it for one made before the plugin loaded. The plugin
//     says so on each request of that session's hook (child, parent_native), and at once when the
//     session is made, so the daemon has it off the mesh before its first call to one of Murmur's
//     tools registers it (#455, #452).
//
// A turn in an idle session needs nothing of the plugin: inside T3 it goes through T3, and outside
// it through `opencode api` (#135). The plugin is in every OpenCode session of the user, so it
// runs the hook only in a clone the user joined (its .git/murmur/joined.json), found from the
// server's directory, as Pi's extension does: elsewhere a tool call waits on no process of
// Murmur's. It looks again at each event until the clone is joined. Not covered: a session moved
// to another clone with `mur join -C` (#200) is looked for in the clone it works in; OpenCode 1,
// whose plugins have another shape, gets no plugin; and OpenCode's commits aren't credited to its
// session, as no tool call is bracketed.

// openCodePlugin is the plugin's text for the mur at exe.
func openCodePlugin(exe string) string {
	cmd, _ := json.Marshal(exe)
	return strings.NewReplacer("{{product}}", brand.Product, "{{command}}", brand.Command, "{{server}}", brand.MCPServer,
		"{{state}}", brand.StateDir, "{{consent}}", consentFile, "{{exe}}", string(cmd)).Replace(openCodeTemplate)
}

// openCodeTemplate is the plugin, with its names left for package brand to fill in. It is plain
// JavaScript with no import of OpenCode's own: a default export of {id, setup} is all OpenCode
// asks.
const openCodeTemplate = `// Written by ` + "`{{command}} join`" + ` ({{product}}, https://github.com/a-Fig/murmur). ` + "`{{command}} join`" + ` writes it again when {{command}} moves,
// and {{command}} when it is updated, so edits here are lost; take this first line out to keep yours.
// It brings an OpenCode session its {{product}} messages: a notice after a tool call while it works,
// and what it hasn't read when its turn ends. It also tells {{product}} which sessions another
// session started, so that they stay out of {{product}}. Delete this file to take it out of OpenCode.
import { spawn } from "node:child_process";
import { existsSync, readFileSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";

const mur = {{exe}};

// joined says the user joined {{product}} in the clone that dir is in, as ` + "`{{command}} join`" + ` records it in
// the clone's .git: outside one, the plugin runs no hook. A few stats, no process.
const joined = (dir) => {
  try {
    for (let d = resolve(dir); ; d = dirname(d)) {
      const dot = join(d, ".git");
      if (existsSync(dot)) {
        let git = dot;
        if (!statSync(dot).isDirectory()) {
          const m = /^gitdir:\s*(.+)$/m.exec(readFileSync(dot, "utf8"));
          if (!m) return false;
          git = resolve(d, m[1].trim());
          const common = join(git, "commondir");
          if (existsSync(common)) git = resolve(git, readFileSync(common, "utf8").trim());
        }
        return existsSync(join(git, "{{state}}", "{{consent}}"));
      }
      if (dirname(d) === d) return false;
    }
  } catch {
    return false;
  }
};

export default {
  id: "{{server}}",
  setup(ctx) {
    const dir = String(ctx?.location?.directory ?? process.cwd());
    let on = false;
    let stopped = false;
    // parents is, for each session seen, the session that started it, "" for one a person started.
    const parents = new Map();
    const parentOf = async (id) => {
      if (parents.has(id)) return parents.get(id);
      try {
        const s = await ctx.session.get({ sessionID: id });
        parents.set(id, String(s?.parentID ?? ""));
      } catch {
        return ""; // asked again next time
      }
      return parents.get(id);
    };

    // hook runs {{product}}'s hook for one of Claude Code's events and gives what it printed, parsed;
    // null on any trouble, or when the server isn't in a joined clone.
    const hook = async (event, sessionID, extra = {}) => {
      if (stopped || !sessionID) return null;
      if (!on && !(on = joined(dir))) return null;
      const parent = await parentOf(sessionID);
      return new Promise((done) => {
        let out = "";
        try {
          const child = spawn(mur, ["hook", "--agent", "opencode"], {
            cwd: dir, stdio: ["pipe", "pipe", "ignore"], windowsHide: true, timeout: 10000,
          });
          child.stdout.on("data", (b) => (out += b));
          child.on("error", () => done(null));
          child.on("close", () => {
            try {
              done(out.trim() ? JSON.parse(out) : null);
            } catch {
              done(null);
            }
          });
          child.stdin.on("error", () => {});
          child.stdin.end(JSON.stringify({
            session_id: sessionID, hook_event_name: event, cwd: dir, child: parent !== "", parent_native: parent, ...extra,
          }));
        } catch {
          done(null);
        }
      });
    };
    const notice = (r) => String(r?.hookSpecificOutput?.additionalContext ?? "");
    // say puts {{product}}'s words in the session as a message of their own. Steered, they go in after
    // the tool calls of a session at work, before its next model request.
    const say = async (sessionID, text, delivery) => {
      try {
        await ctx.session.synthetic(delivery ? { sessionID, text, delivery } : { sessionID, text });
      } catch {}
    };

    ctx.tool.hook("execute.after", async (ev) => {
      const text = notice(await hook("PostToolUse", ev?.sessionID));
      if (text) await say(ev.sessionID, text, "steer");
    });

    (async () => {
      try {
        for await (const ev of ctx.event.subscribe({})) {
          if (stopped) return;
          const type = String(ev?.type ?? "");
          const id = ev?.data?.sessionID;
          if (!id) continue;
          if (type === "session.created") {
            const parent = String(ev.data.parentID ?? "");
            parents.set(id, parent);
            // Said at once, before the session's first call to a tool of {{product}}'s registers it.
            if (parent) hook("SessionStart", id);
          } else if (type === "session.execution.started") {
            hook("UserPromptSubmit", id).then((r) => {
              const text = notice(r);
              if (text) return say(id, text, "steer");
            });
          } else if (type === "session.execution.succeeded") {
            hook("Stop", id).then((r) => {
              if (r?.decision === "block" && r?.reason) return say(id, String(r.reason));
            });
          } else if (type.startsWith("session.execution.")) {
            // Interrupted, or failed: the work ended with no chance to go on.
            hook("Stop", id, { interrupted: true });
          }
        }
      } catch {}
    })();

    return () => {
      stopped = true;
    };
  },
};
`

// openCodeMarker starts every plugin join writes.
const openCodeMarker = "// Written by `" + brand.Command + " join`"

// openCodeExeLine is the line of a plugin join wrote that names the mur it runs.
var openCodeExeLine = regexp.MustCompile(`(?m)^const mur = ("(?:[^"\\]|\\.)*");\s*$`)

func openCodePluginPath(dir string) string {
	return filepath.Join(dir, "plugins", brand.MCPServer+".js")
}

// writeOpenCodePlugin writes Murmur's plugin into OpenCode's config directory dir, for the mur at
// exe. It says whether it wrote one. A file there that join didn't write is left alone, with an
// error.
func writeOpenCodePlugin(dir, exe string) (wrote bool, err error) {
	path := openCodePluginPath(dir)
	want := openCodePlugin(exe)
	b, err := os.ReadFile(path)
	switch {
	case err == nil && strings.ReplaceAll(string(b), "\r\n", "\n") == want:
		return false, nil
	case err == nil && !strings.HasPrefix(string(b), openCodeMarker):
		return false, fmt.Errorf("%s is there and isn't the one join writes, so it was left alone", path)
	case err != nil && !os.IsNotExist(err):
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := writeAtomic(path, []byte(want), 0o644); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return true, nil
}

// RefreshOpenCode brings Murmur's OpenCode plugin up to this binary's, as OpenCode's MCP server
// starts: an update replaces the binary and doesn't run join again, so a plugin from an older one
// would stay as it was (as RefreshPi does for Pi's extension, #205). It rewrites only a plugin
// join wrote, that runs this very binary, and whose text is another's. OpenCode loads plugins
// again when the folder changes. It says whether it rewrote it.
func RefreshOpenCode() (bool, error) {
	exe, err := os.Executable()
	if err != nil {
		return false, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(home, ".config")
	}
	return refreshOpenCodeAt(filepath.Join(config, "opencode"), exe)
}

func refreshOpenCodeAt(dir, exe string) (bool, error) {
	path := openCodePluginPath(dir)
	b, err := os.ReadFile(path)
	if err != nil {
		return false, nil // none: join writes one
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	m := openCodeExeLine.FindStringSubmatch(text)
	if !strings.HasPrefix(text, openCodeMarker) || m == nil {
		return false, nil
	}
	var named string
	if json.Unmarshal([]byte(m[1]), &named) != nil || !sameFile(named, exe) {
		return false, nil
	}
	want := openCodePlugin(named)
	if text == want {
		return false, nil
	}
	if err := writeAtomic(path, []byte(want), 0o644); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	return true, nil
}

// removeOpenCodePlugin deletes Murmur's plugin from OpenCode's config directory dir, and the
// plugins folder if that leaves it empty. One join didn't write is a user's own, and stays.
func removeOpenCodePlugin(dir string) (gone bool, note string, err error) {
	path := openCodePluginPath(dir)
	gone, note, err = removeOwned(path, func(b []byte) bool { return bytes.HasPrefix(b, []byte(openCodeMarker)) })
	if gone {
		os.Remove(filepath.Dir(path)) // stays if anything else is in it
	}
	return gone, note, err
}
