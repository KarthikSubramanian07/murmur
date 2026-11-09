package join

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/jsonedit"
)

// Pi (the pi coding agent, 0.99) has MCP built in: servers in mcp.json in its agent directory
// (~/.pi/agent, or $PI_CODING_AGENT_DIR), one server process per session, with Pi's environment.
// But nothing in that process says which session it is: Pi gives the session's ID and file only to
// its own shell tools, and T3 Code knows a Pi thread by the session's file. So join registers
// Murmur's server through a Pi extension instead, a file of Murmur's own in the agent directory's
// extensions/, which Pi loads in every session: it registers the server as Pi loads it, which makes
// it a server Pi waits for before the first prompt, and again at each session's start with the
// session's file in MURMUR_PI_SESSION_FILE (pi.registerMcpServer, docs/extensions.md and
// docs/mcp.md of Pi's package, read 2026-11-01; Pi 0.99.0 showed it connected with its five tools,
// 2026-11-01). "exposure": "direct" lists the tools themselves; Pi's
// default hides them behind a tool of its own. Pi asks before no tool call; in T3, T3's own Pi
// extension asks before each one outside full-access mode. A server named murmur in mcp.json would
// win over the extension's, and its sessions would all look like one.

// The extension also brings a session its messages, as Murmur's hook does in Claude Code (#202,
// #205): Pi has no hooks of that kind, so the extension runs `mur hook --agent pi` at the same
// moments, with Claude Code's event names and the session's file for its ID (package hook), and
// opens a socket of its own that takes Claude Code's messaging-socket lines (package msgsock), whose
// address and token it gives the MCP server in its environment as Claude Code does. From Pi's
// docs/extensions.md and the types of @earendil-works/pi-coding-agent 1.5.0, read 2026-11-09, and
// run with Pi 1.5.0 against a stub model the same day (the PR that added this says what was run):
//   - before_agent_start, which every prompt and every turn the extension starts goes through, is
//     UserPromptSubmit: the session is at work, and a notice goes in as the run's message;
//   - tool_result is PostToolUse: a notice is sent with deliverAs "steer", which Pi puts in after
//     the tool calls and before the next model request;
//   - agent_before_settle, the last point at which Pi lets an extension add entries and continue,
//     is Stop: what the hook gives back keeps the work going, as a custom_message entry and
//     continue: true. Pi skips it after an abort (Escape), and the extension skips it after an
//     error, which ends Pi's run whatever it asks; the session then stays at work for the daemon,
//     as an interrupted Claude Code session does, until its next prompt;
//   - a message on the socket is sent with triggerTurn, which starts a turn in an idle session, and
//     deliverAs "followUp", which holds it until a busy one finishes rather than steering it.
// Everything Murmur says goes in as a custom message with customType "murmur", shown in the
// session, which the model reads in the user's role but which Pi doesn't take for the user's
// typing, as it would sendUserMessage's (#205). Under T3 the socket is there too, and unused: the
// daemon reaches a T3 session through T3 (internal/daemon, viaOf). A session without a file
// (`pi --no-session`) has nothing to give the hook, so it gets no notices, only turns through the
// socket, which a busy one takes when it finishes. Not covered: Pi's commits aren't credited to
// its session, as no tool call is bracketed (Claude Code's hook snapshots before and after each).

// piExtension is the extension's text for the mur at exe.
func piExtension(exe string) string {
	cmd, _ := json.Marshal(exe)
	return strings.NewReplacer("{{product}}", brand.Product, "{{command}}", brand.Command, "{{server}}", brand.MCPServer,
		"{{exe}}", string(cmd)).Replace(piTemplate)
}

// piTemplate is the extension, with its names left for package brand to fill in.
const piTemplate = `// Written by ` + "`{{command}} join`" + ` ({{product}}, https://github.com/a-Fig/murmur). ` + "`{{command}} join`" + ` writes it again when {{command}} moves.
// It registers {{product}}'s MCP server for each Pi session, with the session's file in its
// environment: that is how {{product}} tells Pi sessions apart, and finds a session's T3 Code thread.
// It also brings the session its messages: a notice after a tool call while it works, what it hasn't
// read when its work ends, and a turn when it is idle, through a socket it opens for the session.
// Delete this file to take {{product}} out of Pi.
import { spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { chmodSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";

const mur = {{exe}};
const windows = process.platform === "win32";

export default function (pi: any) {
  let file = "";
  let sock = "";
  let token = "";
  let server: any = null;
  const register = () =>
    pi.registerMcpServer("{{server}}", {
      command: mur,
      args: ["mcp", "--agent", "pi"],
      exposure: "direct",
      env: { MURMUR_PI_SESSION_FILE: file, MURMUR_PI_MESSAGING_SOCKET: sock, MURMUR_PI_MESSAGING_TOKEN: token },
    });
  // Registered as Pi loads, the server is one Pi waits for before the first prompt; registered
  // again when the session starts, it has the session's file and socket.
  register();

  // What {{product}} says: a message of its own, shown in the session.
  const said = (content: string) => ({ customType: "{{server}}", content, display: true });

  // hook runs {{product}}'s hook for one of Claude Code's events and gives what it printed, parsed;
  // null on any trouble, or when the session has no file to be known by.
  const hook = (event: string, ctx: any): Promise<any> =>
    new Promise((resolve) => {
      if (!file) return resolve(null);
      let out = "";
      try {
        const child = spawn(mur, ["hook", "--agent", "pi"], {
          cwd: ctx?.cwd, stdio: ["pipe", "pipe", "ignore"], windowsHide: true, timeout: 10000, signal: ctx?.signal,
        });
        child.stdout.on("data", (b: any) => (out += b));
        child.on("error", () => resolve(null));
        child.on("close", () => {
          try {
            resolve(out.trim() ? JSON.parse(out) : null);
          } catch {
            resolve(null);
          }
        });
        child.stdin.on("error", () => {});
        child.stdin.end(JSON.stringify({ session_id: file, hook_event_name: event, cwd: ctx?.cwd }));
      } catch {
        resolve(null);
      }
    });
  const notice = (r: any) => String(r?.hookSpecificOutput?.additionalContext ?? "");

  const close = () => {
    try {
      if (server) server.close();
      if (sock && !windows) rmSync(sock, { force: true });
    } catch {}
    server = null;
    sock = token = "";
  };
  // The socket takes what Claude Code's messaging socket does: a line {"type":"auth","token":...},
  // then lines {"type":"user","message":{"content":...}}. Without it, messages wait for the hook.
  const listen = (): Promise<void> =>
    new Promise((resolve) => {
      const id = randomBytes(8).toString("hex");
      const path = windows ? "\\\\.\\pipe\\murmur-pi-" + id : join(tmpdir(), "murmur-pi-" + id + ".sock");
      const key = randomBytes(24).toString("hex");
      const srv = createServer((conn: any) => {
        let buf = "";
        let authed = false;
        conn.setEncoding("utf8");
        conn.on("error", () => {});
        conn.on("data", (chunk: string) => {
          buf += chunk;
          if (buf.length > 4 << 20) return conn.destroy();
          for (let i = buf.indexOf("\n"); i >= 0; i = buf.indexOf("\n")) {
            let rec: any;
            try {
              rec = JSON.parse(buf.slice(0, i));
            } catch {
              return conn.destroy();
            }
            buf = buf.slice(i + 1);
            if (!authed) {
              if (rec?.type !== "auth" || rec?.token !== key) return conn.destroy();
              authed = true;
            } else if (rec?.type === "user" && typeof rec?.message?.content === "string" && rec.message.content) {
              // A turn if the session is idle; held until it finishes if it is busy, not steered into it.
              pi.sendMessage(said(rec.message.content), { triggerTurn: true, deliverAs: "followUp" });
            }
          }
        });
      });
      srv.on("error", () => resolve());
      srv.listen(path, () => {
        try {
          if (!windows) chmodSync(path, 0o600);
        } catch {}
        srv.unref();
        server = srv;
        sock = path;
        token = key;
        resolve();
      });
    });

  pi.on("session_start", async (_event: any, ctx: any) => {
    close();
    file = String(ctx?.sessionManager?.getSessionFile?.() ?? "");
    await listen();
    register();
  });
  pi.on("session_shutdown", () => close());
  pi.on("before_agent_start", async (_event: any, ctx: any) => {
    const text = notice(await hook("UserPromptSubmit", ctx));
    if (text) return { message: said(text) };
  });
  pi.on("tool_result", async (_event: any, ctx: any) => {
    const text = notice(await hook("PostToolUse", ctx));
    if (text) pi.sendMessage(said(text), { deliverAs: "steer" });
  });
  pi.on("agent_before_settle", async (event: any, ctx: any) => {
    if (event?.outcome !== "completed") return;
    const r = await hook("Stop", ctx);
    if (r?.decision !== "block" || !r?.reason) return;
    return { entries: [...(event.entries ?? []), { type: "custom_message", ...said(String(r.reason)) }], continue: true };
  });
}
`

func piExtensionPath(dir string) string {
	return filepath.Join(dir, "extensions", brand.MCPServer+".ts")
}

// registerPiAt writes Murmur's extension into Pi's agent directory h. Pi isn't touched where neither
// its command nor the directory is there.
func (e Env) registerPiAt(h agentHome) (did []string, fresh bool, err error) {
	if h.Dir == "" {
		return nil, false, nil
	}
	if _, lerr := e.look("pi"); lerr != nil && h.Bin == "" && !isDir(h.Dir) {
		return nil, false, nil
	}
	path := piExtensionPath(h.Dir)
	want := piExtension(e.Exe)
	b, err := os.ReadFile(path)
	switch {
	case err == nil && strings.ReplaceAll(string(b), "\r\n", "\n") == want:
		return nil, false, piMCPConflict(h.Dir)
	case err == nil && !strings.HasPrefix(string(b), "// Written by `"+brand.Command+" join`"):
		return nil, false, fmt.Errorf("%s is there and isn't the one join writes, so it was left alone", path)
	case err != nil && !os.IsNotExist(err):
		return nil, false, err
	}
	if err := writeAtomic(path, []byte(want), 0o644); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	return []string{h.Label + ": registered the " + brand.MCPServer + " MCP server for you, in every project, through an extension (" + path + ")"}, true, piMCPConflict(h.Dir)
}

// piMCPConflict is an error if Pi's mcp.json in dir has a server named murmur, which Pi runs instead
// of the extension's: with whatever command it names, and without the session's file (Codex's
// review of #136). join leaves the user's file alone and says what to delete.
func piMCPConflict(dir string) error {
	path := filepath.Join(dir, "mcp.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if _, found, _ := jsonedit.GetIn(string(b), []string{"mcpServers", brand.MCPServer}); found {
		return fmt.Errorf("%s has a server named %s, which Pi runs instead of %s's extension: delete it from there", path, brand.MCPServer, brand.Product)
	}
	return nil
}

func piRemoval(_ Env, h agentHome) string {
	if path := piExtensionPath(h.Dir); exists(path) {
		return "delete " + path
	}
	return ""
}
