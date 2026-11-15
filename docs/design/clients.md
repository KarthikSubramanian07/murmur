# Other agents and their hooks

### Other agents

Added 2026-11-01 (#128), at Tyler's request: OpenCode, Muse Code, Pi and Grok, in T3 Code and on
their own. Each was looked at in its installed binary, its documentation and probe runs (OpenCode
2.2.0, Muse 1.4.14, Pi 0.99.0 on Tyler's laptop; Grok Build from its documentation and source only,
since it wasn't installed). The details, and where each came from, are in the comments of
`internal/join` (one file per agent) and `internal/mcpserver`.

| Agent | Where join writes | How a session is told apart | Inside T3 |
|---|---|---|---|
| OpenCode | `~/.config/opencode/opencode.jsonc` if there (OpenCode reads it after `opencode.json`, whose own murmur entry is made the same), else `opencode.json`, in the shape the installed OpenCode loads, from `opencode --version`: for 2.x, `mcp.servers.murmur` with `"codemode": false` and a `murmur_*` allow rule kept last in the `permissions` list; for 1.x, or a version join can't learn, `mcp.murmur` with `"enabled": true` and `"murmur_*": "allow"` kept last in the `permission` object, which 2.x loads too. In both the last matching rule counts. OpenCode 1.x refuses a whole config that has `permissions` (#388), so a rerun moves Murmur's entries to the shape it now writes | One server process serves every session in a directory; each call names its session in `_meta["ai.opencode/sessionID"]`, and the server keeps a registration per session that calls | The session ID is T3's native ID for the thread |
| Muse Code | `mcpServers.murmur` in `~/.config/muse/settings.json`, `"mode": "optional"`, `env` forwarding `T3CODE_CLI_PATH` and, on Windows, the home variables, as `${VAR:-}` | One process per session, with `MUSE_SESSION_ID` | The same ID |
| Pi | an extension, `extensions/murmur.ts` in `~/.pi/agent` (a `~` in `PI_CODING_AGENT_DIR` is the home, as for Pi), that registers the server with the session's file in `MURMUR_PI_SESSION_FILE`; a `murmur` server in Pi's `mcp.json`, which Pi would run instead, is reported | One process per session | T3 knows a Pi thread by that file (not tried: Pi is off in T3 on the laptop) |
| Grok | `[mcp_servers.murmur]` in `~/.grok/config.toml`, and `MCPTool(murmur__*)` in `[permission] allow` | One process per session, with `GROK_SESSION_ID` | Expected to be the ACP session ID T3 records (not tried) |

- **Edited, not rewritten.** JSON and JSONC files are edited as text (`jsonedit.SetIn`,
  `AppendIn`): one member or item changes and every other byte, comments and alignment included,
  stays. A file in a form join can't read safely (not an object, a path through a list, a key
  given twice, Muse's older `mcp_servers` beside which a new key would drop every server) is a
  reported problem. Grok's TOML goes through Codex's editor. Pi's extension is Murmur's own file,
  written whole, and one of that name that isn't Murmur's is left alone.
- **Identity.** Claude Code's variables count only for Claude Code: Pi and OpenCode pass their own
  environment on to their servers, and one started from a Claude Code session carries that
  session's `CLAUDE_CODE_SESSION_ID`. An agent's own session ID becomes the session's Murmur ID by
  hash, since Muse's and OpenCode's IDs begin with the time and two of them often share six
  characters. A T3 instance with no directory of its own gets the user's default, not the directory
  of the instance that ran join. A session of the shared OpenCode server is registered at its first call and dropped
  after 30 minutes without one, as a stopped session is; its T3 route outlives that as any does.
- **Delivery.** Inside T3 the existing `t3 thread send` handoff needed nothing new: an OpenCode and
  a Muse thread each took a message as a turn and answered it with `send`, nobody typing
  (2026-11-01). Outside T3, Muse and Grok have no way in that Murmur uses yet (Muse's session
  messaging isn't on Windows, and no way to start a turn in Grok was found), so a message waits in
  the inbox. OpenCode 1 and Muse have no hook of Murmur's, so no notice either; Grok has one (below). Pi
  has both since 2026-11-09 (#205), and OpenCode 2 takes a turn through its server since
  2026-11-11 (#135): see "Pi's extension" and "The client's command line" under "Delivering a
  message as a turn".
- **OpenCode 2's plugin (2026-11-12, #431, #455).** OpenCode 2 has no hooks of Claude Code's kind,
  so join writes `plugins/murmur.js` into its config directory, which OpenCode loads with no entry
  in `opencode.json`. The plugin runs `mur hook --agent opencode` after each tool call, when a turn
  starts and when it ends, with OpenCode's session ID, so an OpenCode 2 session is told of a
  message while it works and handed what is unread at its turn's end, as Claude Code is.
  - *How the words go in.* `ctx.session.synthetic` with `delivery: "steer"` puts the notice after
    the tool's result and before the next model request, as a message of its own. Appended to the
    tool's result instead, a model took it for text planted in the file it had read and refused it.
    At a turn's end the session is idle, and the same call starts another turn. What it costs:
    OpenCode's terminal UI shows the answer to a synthetic message and not the message.
  - *Order.* Each hook is a process, so the plugin runs one session's hooks one after another. A
    turn's start that reached the daemon after its end would leave an idle session looking busy.
  - *Children.* A session the `subagent` tool started carries `parentID`. The plugin says so on
    every hook request of that session (`child`, `parentNative`, #473), and once as the session is
    made, before its first call to one of Murmur's tools registers it; the daemon marks a session
    named so as it registers. So an OpenCode child is never listed, and its `send` is refused with
    its parent's name. The gap #487 describes for Codex is left only where the daemon didn't hear
    the plugin: a child made while no daemon ran, or before a daemon restart, is on the mesh from
    its first call to one of Murmur's tools until its next tool call ends.
  - *Tried* on OpenCode 2.2.0 against a private `opencode serve` in a joined scratch clone
    (2026-11-12): a message sent mid-turn came as a notice after the next tool call; at the turn's
    end the session was handed it whole, read a file and answered with `send`, nobody typing; a
    subagent that called `who` and `send` never showed in another session's `who`, and its `send`
    was refused. Not tried: a thread T3 started, the terminal UI on the shared background server,
    and whether T3's sidebar shows a child session as a thread of its own (Tyler's rule, #452,
    keeps such a one on the mesh; the plugin would take it off).
  - *Not covered.* `opencode run` exits when its turn ends, so what the hook hands over there is
    marked read and never acted on. OpenCode 1 loads plugins of another shape and gets none.
- **Titles.** Inside T3 the thread's title is read as for any thread. Outside T3 none is read for
  these agents: their IDs aren't Codex threads.
- **Tool approval.** OpenCode and Grok have a rule for it, which join writes; Muse and Pi have none.
  In T3 outside full-access mode, Muse and T3's Pi extension ask before each call.

### Claude Code's hook is per user

Changed 2026-11-11 (#422). Until then join wrote Claude Code's hook in
`.claude/settings.local.json` in each worktree of the joined clone. A session that joined a clone it
doesn't work in (`mur join -C`, "A session that started outside the repo") never read that file, so
it got no notice while busy, no `Stop` to keep its turn going, and the daemon never saw it at work
or idle. Tyler's rule (2026-11-11): every harness behaves as closely to T3 Code as it can, wherever
the session runs.

- **Where it is.** Join writes the hook in `settings.json` of each Claude Code home, beside the
  allow rule, so Claude Code runs it in every project, as Codex and Grok run theirs. Join and leave
  take the older hook out of the clone's worktrees, since Claude Code would run both. A join that
  couldn't write a Claude Code home's settings leaves the older hook, which is then the only one;
  and a worktree's file that is the user's own link to those settings is passed over, since the
  hook in it is the new one. A leave keeps the per-user hook, like the server; `mur uninstall`
  removes it.
- **Which clone.** The hook used to take its clone from its working directory. Now it reads the
  record its session's MCP server publishes (`murmur/sessions/`), found by `CLAUDE_PID`, or for
  Codex and Grok by the nearest of the hook's ancestors that started a server. The record names
  the clone, or says the session is in no repository, and the hook then asks nothing. Where no
  record is surely the session's, it goes by its working directory as before.
- **Whose record.** Claude Code names its own process, which runs one session, so its one record
  is the session's. Another agent's hook has only its ancestors to go by, and a record found there
  may not be its session's: it may be a sibling's in a process that runs several sessions, each
  with a server (Codex's app server), or that of an agent that started this one (a Codex run from
  Claude Code's shell). Taking it would send the hook to the wrong daemon, where it could be given
  that session's messages. So the record now carries the agent's own session ID (Grok's, from its
  environment; Codex's thread ID, which its server learns from the first call to one of Murmur's
  tools), and another agent's record counts only when it has the hook's ID. A Codex session that
  joined from elsewhere so gets its notices from its first Murmur tool call on.
- **Not covered.** Pi: its extension runs the hook only where the working directory's clone is
  joined, so a Pi session joined from elsewhere gets turns through its socket and no notice while
  busy. Commits made in the other repo aren't credited: the daemon watches only its own clone
  (#405).
- **What it costs.** Every Claude Code tool call on the machine now starts `mur hook` up to twice,
  in repos that never use Murmur too. Measured on the Windows laptop, 2026-11-11, 30 runs each,
  with 41 records in the directory: 14 ms for a call the hook skips before looking anywhere (a
  `Read`'s `PreToolUse`), 19 ms where it reads the records and finds its session in no repository.
  Claude Code's own shell for the hook comes on top and wasn't measured. With no record and a
  working directory outside any repo it asks git, 55 ms.

### Codex's and Grok's hook

Added 2026-11-09 (#225, part of #205): the hook that tells Claude Code of messages mid-turn and
keeps its turn going at Stop (#202) runs in Codex and Grok too, as `mur hook codex` and
`mur hook grok`. Both take Claude Code's payload and answers; Grok spells the session ID
`sessionId`.

- **Codex.** Join puts the hook in `hooks.json` in each Codex home, on three events: a prompt
  (`UserPromptSubmit`), after every tool call (`PostToolUse`) and at the end of a turn (`Stop`), the
  fewest that mark the session at work, bring notices and keep a turn going. Not `PreToolUse`:
  Codex's commits aren't credited (#21). Codex runs a hook only once its user has approved it, on a
  "Hooks need review" screen in its own TUI, and T3 Code, where most Codex sessions run, never shows
  that screen, so there the hook would never run. Tyler's decision (2026-11-08): join writes the
  approval itself, as `[hooks.state.'<hooks.json>:<event>:<group>:<handler>'] trusted_hash` in the
  home's `config.toml`, the entry Codex writes when the user approves. The hash is SHA-256 of the
  handler's settings as compact JSON with sorted keys (codex-rs `hooks/src/engine/discovery.rs`
  and `config/src/fingerprint.rs`, rust-v0.166.0 and rust-v0.167.0), and the tests check it against
  hashes Codex wrote. The key names the group by its place in the event's list, so join edits
  `hooks.json` in place: its group stays where it is, and goes last only when it is new, so the
  user's own hooks keep their keys. The key spells the path as Codex does: a home `CODEX_HOME`
  names (T3's instances too) it canonicalizes, links resolved and Windows short names made long,
  and `~/.codex` it takes as it is (codex-rs `utils/home-dir`, rust-v0.167.0). A key in any other
  spelling is never read, and Codex asks about the hook. If a Codex release hashes differently, the
  hook silently stops running, which the daemon shows (below). The handler's `statusMessage` is
  "Murmur", which is how Codex names it in its hooks list and how join finds its own entries again.
  Seen on Codex 0.167.0, 2026-11-09, in a throwaway Codex home joined by `mur join`: no review
  screen at three starts of the TUI, `/hooks` listing the hooks as active, and the notice reaching
  the model mid-turn (Codex's rollout records it as `hooks.additional_context`) in the TUI, in
  `codex exec` and in a T3 Codex thread, with `Stop` keeping the turn going with the message
  whole. A broken `trusted_hash` showed as "hook not running" a minute after the session's first
  Murmur call.
- **Grok.** Grok runs every file in `~/.grok/hooks/` without asking, where a project's hooks wait
  for folder trust (Grok Build's `docs/user-guide/10-hooks.md`, read 2026-11-09), so join writes
  `murmur.json` there, on `PostToolUse` and `Stop`. Not on a prompt: Grok discards what that hook
  prints, so notices handed to it would be marked delivered and lost. Grok also fires `Stop` when a
  session closes (`reason` other than `end_turn`) and ignores the answer, so the hook doesn't ask to
  keep that one going, and what the daemon holds is handed over as a turn instead. From Grok's
  documentation and source only: Grok isn't installed on the laptop, so none of it has run.
- **The command line.** These agents run a hook through a shell. On Windows Codex's is PowerShell
  (cmd where there is none), and PowerShell won't run a quoted path followed by arguments
  ("Unexpected token", so the Claude Code hook's form exited 1 under Codex 0.166.0). So the path is
  bare with its backslashes, which PowerShell and cmd both run (cmd takes a forward slash for a
  switch); its 8.3 short form if it has a space or anything else either shell reads as syntax; and
  `& '…'`, PowerShell's call operator, only if neither works. Elsewhere it is single-quoted for sh.
- **Which session.** Codex's MCP server learns its thread's ID only from a call to one of Murmur's
  tools (`_meta.threadId`), so before that the daemon can't match the hook's `session_id` to it.
  The hook also sends its ancestor processes, and the MCP server's parent is among them: the Codex
  process that runs the thread, which is a shared app server, both for T3 and for Codex's own TUI.
  If that process has one session of the agent, with no ID yet, it takes the hook's (as
  `hookNative`) and is found by it from then on. A process with several (T3's Codex threads) can't
  be told apart, even when only one has no ID, since a thread whose MCP server hasn't reached the
  daemon yet would look the same: the hook speaks for none until each calls a Murmur tool, but all
  count as having a hook. The ID the MCP server learns overrides one taken from a hook (Sol's
  review, 2026-11-09, found a wrong one could otherwise take another thread's notices).
- **Hook not running.** A session of an agent the hook runs in (Claude Code, Codex, Grok, and Pi
  through the extension join writes)
  that called one of Murmur's tools over a minute ago, with no hook of it reporting since the
  daemon started, is marked: `who` adds ", hook not running" to its line and tells a session its
  own, the page puts a badge on its card, and `send` says the recipient won't be told while it
  works. Told from the daemon's side, not by checking the hash, because the ways it breaks (a new
  Codex hashing differently, a hook that fails, a worktree made after joining) leave no error to
  find. A session's own line asks it to have its user run `mur join` again, which writes the hook
  and its trust anew.
- **In every repo.** Both hooks are per user, like the server, so they run in every repo the
  agent works in. Where Murmur isn't joined the hook finds no daemon and prints nothing, which cost
  70-100 ms a tool call on the laptop (2026-11-08), plus the shell the agent starts it in. A leave
  leaves them, as it leaves the server, and its output says how to remove them.
