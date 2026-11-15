# Joining a repo

## Joining a repo

Decided 2026-10-29 (#55), built the same day. Tyler's requirement: setup should feel like magic,
and T3 Code users are the main customers. The user should still know Murmur is running and choose to
join. So a repo that uses Murmur says so, in a section of `AGENTS.md` and `CLAUDE.md` that an agent
reads when it starts; an agent that has no Murmur tools asks its user whether to join; and if the
user says yes, the agent installs mur and runs `mur join`, which does the rest. Tyler also decided
that messaging gets no command-line commands (`who`, `send`, `inbox`, `log` were removed): "If
everyone is using the CLI, there will be no one to message." Agents message through MCP, humans look
at the page (`mur ui`), and the commands left are `init`, `join`, `ui`, `stop`, `uninstall`,
`version` and `help`.

- **The marker.** `mur init` writes a section between `<!-- murmur:start -->` and `<!-- murmur:end
  -->` into both files, creates either if it is missing, and rewrites the section in place on a
  rerun. The section is six lines, since every agent that opens the repo reads it whole (Tyler,
  2026-10-29, after a first version of about twenty): it says the repo uses Murmur and links two
  pages in `docs/agents/`, by raw GitHub address. `join.md` is for an agent without the tools: ask
  the user, say what joining does, install mur (the one-line command for macOS and Linux or for
  Windows PowerShell), run `mur join`, tell the user what it printed. `use.md` is for an agent with
  them: keep `status` set, check `who` before sizeable work, answer a message with `send`, and
  `inbox` for history and as the fallback. The addresses work without login, since the repo is
  public. A test holds the
  addresses to files that exist. `init` writes no other file. An older `init` had put an `.mcp.json`
  entry and a hook in `.claude/settings.json`; `init` removes only Murmur's own entries from those,
  deletes a file that is left empty, and says so. Committing installs nothing on a teammate's machine.
- **`init` pushes its commit to the default branch** (Tyler, 2026-11-01, #119). Until then it edited
  the working tree and said "review and commit"; run in a T3 worktree, the section landed on that
  worktree's branch at best, so teammates' agents on main never read it. Now `init` fetches the
  default branch of the clone's remote (the bus's remote, `murmur.remote` else `origin`; the branch
  the remote's HEAD names), applies the same edits to that commit's files, builds the tree with a
  temporary index (`GIT_INDEX_FILE`) and `commit-tree`, and pushes `<sha>:refs/heads/<default>`,
  never forced: the same rule as the bus, no working tree or index touched. A push rejected because
  the branch moved is rebuilt on the new tip, up to 3 tries. One the server refuses for a reason of
  its own (a protected branch, a hook) goes to a new branch, `murmur-init`, and `init` prints the
  GitHub page that opens a pull request from it; a `murmur-init` already there with the section
  counts as pending, and one without it stops `init`. A failure to push at all (no permission, no
  network) is reported, not worked around. Nothing changes when the branch already has the section.
  The checkout `init` ran in is fast-forwarded to the commit only when it is on the default branch,
  behind the commit, with no local change to the four files (git refuses a pull over an
  uncommitted change to a file the pull brings, even an identical one, checked 2026-11-01);
  otherwise it is left alone and gets the section when the default branch is merged in. The push
  skips the repo's pre-push hook, as bus pushes do: the hook would run against a checkout that
  isn't this commit. `init --no-push` is the old behavior, and refuses as the push does a file
  that a symbolic link takes out of the repo (2026-11-09). `docs/agents/setup.md` is the page an
  agent follows when its user asks it to set Murmur up; the README links it.
- **What `mur join` writes.** It refuses in a repo without the marker. Everything it writes is
  outside the repo's tracked files:

  | What | Where | Per |
  |---|---|---|
  | Consent | `.git/murmur/joined.json` (mode 0600) | clone, so every worktree of it |
  | The MCP server, for Claude Code | `claude mcp add --scope user murmur -- <absolute path of mur> mcp`, which writes `.claude.json` in `$CLAUDE_CONFIG_DIR`, else the home directory | user |
  | Permission for its tools | `mcp__murmur` in `permissions.allow` of `settings.json` next to it | user |
  | The MCP server, for Codex | a `[mcp_servers.murmur]` table in `$CODEX_HOME/config.toml`, else `~/.codex/config.toml`: the command, `args = ["mcp", "--agent", "codex"]`, `env_vars = ["T3CODE_CLI_PATH", "CODEX_HOME"]` and `default_tools_approval_mode = "approve"` | user |
  | The MCP server, for OpenCode, Muse Code, Pi and Grok | see "Other agents", below | user |
  | The skills, `murmur` and `murmur-pro` (`internal/skills`) | `skills/<name>/SKILL.md` in the home of Claude Code, Codex, OpenCode and Pi; Muse Code and Grok document no skills folder (2026-11-08) | user |
  | The hook for Claude Code, on `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `UserPromptSubmit` and `Stop` (`setup.HookEvents`) | `settings.json` in each Claude Code home (see "Claude Code's hook is per user", below) | user |
  | The hook for Codex, on `UserPromptSubmit`, `PostToolUse` and `Stop`, approved (see "Codex's and Grok's hook") | `hooks.json` in each Codex home, its trust in `config.toml` there | user |
  | The hook for Grok, on `PostToolUse` and `Stop` | `hooks/murmur.json` in Grok's home | user |
  | The daemon | started, if none is running | clone |

  The skills carry the judgment the tool descriptions don't: when Murmur is worth a turn, and how
  to coordinate over several messages (#206). A SKILL.md of the same name without the skills'
  marker comment is the user's own, and is left alone; one with it is rewritten to this binary's.
  A leave leaves them, like the server, and says how to delete them. Unlike the server, they cost
  every session of those agents their descriptions' worth of context in any repo, so each
  description starts by saying it applies only where the repo has the Murmur section. The folders
  are from each agent's documentation, not tried in a session (2026-11-08). OpenCode also reads
  `~/.claude/skills`, so with Claude Code beside it it sees each skill twice, the same text.

  A client that isn't installed is skipped. Each file is edited, never replaced: other servers,
  settings and comments stay, and a form it can't edit safely (a dotted `mcp_servers.murmur.command`
  key in Codex's TOML, a `permissions` that isn't an object) is a reported problem, not a guess.
  `codex mcp add` is not used, because it rewrites the whole file. The path is absolute so the
  server starts whatever PATH the client has. The hook command is the path in double quotes with
  forward slashes (`"C:/Users/a b/mur.exe" hook`), because Claude Code runs hook commands through a
  shell, which would split a path with a space, eat the backslashes of a Windows one, and expand a
  `$` or backtick in it; those are escaped. Measured to run under Claude Code 2.1.312 with a space
  in the path, 2026-10-29. A config file that is a symbolic link (a dotfiles repository's, say) is
  written through, one that points at a file not yet there included: the file it points to is
  replaced or created and the link stays; a hook file that is a link is emptied, never deleted.
  A hook file the repo tracks, or a `.claude` it tracks as a link, isn't the user's, and gets no
  hook (2026-11-09): a committed link points wherever the commit chose, `~/.claude/settings.json`
  included, and a tracked file would take this machine's path to mur into the next commit. Names
  are matched ignoring case, as macOS's and Windows' disks match them. It is a note under "Not
  done", not a problem: repos do commit that file by accident, and a rerun couldn't fix it.
  Run again, `join` changes nothing and says so; it fills in what is missing and repoints an entry
  whose binary moved. A worktree made after joining has no hook until `mur join` runs in it again.
  `mur join --leave` removes the consent and the hooks and stops the daemon. It leaves the server
  registered, since that is per user and does nothing where the user hasn't joined; its output says
  how to remove it.
- **`mur uninstall` removes Murmur from the machine** (Tyler, 2026-11-10, #399). A leave can't remove
  the per-user entries, because one entry serves every repo the user joined and a leave can't tell
  whether its repo was the last. Uninstall can, because it reads the record of this machine's clones
  (`internal/clones`). It refuses while a clone on the record is still joined and names each, or
  with `--leave` leaves each first, and stops if a leave didn't take the consent away. It then takes
  out what join wrote per user in every agent configuration join registers in, T3's instances
  included. That covers the server entry and allow rule in each agent, both OpenCode shapes, Codex's
  hook groups and their trust entries, Grok's hook file, Pi's extension, the skills, and a notice hook
  in Claude Code's own `settings.json`. The edits follow join's rules: files read as regular files
  under jsonedit's cap, links written through, JSON re-parsed before it is written, a form join
  wouldn't edit refused, and the user's other entries kept byte for byte. The Claude Code server
  goes through `claude mcp remove` where the command exists, as join adds it through `claude mcp add`. Codex keys
  a hook's trust by its group's place, so the user's own groups after Murmur's move up one and their
  trust entries are renamed with them, so Codex doesn't ask about them again. Every Codex edit is
  made before any is written, with each `hooks.json` or `config.toml` that two homes share edited
  once, and `hooks.json` goes back as it was if a `config.toml` can't be written: once Murmur's
  groups are out of it, nothing tells a rerun where they were. Uninstall then leaves again every
  other clone on the record, which takes out a hook an earlier leave left and stops a daemon
  through `client.Stop`. A daemon from before 0.3 can't be asked (#389), and the error names its
  process ID. Last, it deletes the record's file of each clone whose cleanup finished. A clone whose
  cleanup didn't finish, or whose folder isn't there now (deleted, or on a drive not mounted), stays
  on the record, so a rerun finds it (Sol's reviews, 2026-11-10). Then it deletes Murmur's folder in
  the user's config directory, which also holds the records MCP servers publish of themselves
  (`internal/here`) and `id`, the random user ID the check-ins of 0.2.3 sent, which nothing reads
  since #199 (#430). A server still running keeps its record, since it reads its moves there until
  its session ends, so the folder stays with it, and the report names it and says why. It leaves what isn't one
  user's or can't go while it runs: each repo's committed section, each clone's `.git/murmur` and
  bus refs, and the binary itself, which Windows can't delete while it runs, so it prints the
  command that does. A clone that joined before the record existed and whose daemon hasn't started
  since isn't on the record, so uninstall can't see it. Its server entry is gone anyway, so nothing
  runs there.
- **What it prints.** What it did, what went wrong, and "Tell your user", chosen from which agent ran
  it (see Reloading MCP servers below), so the agent can say exactly what to do next.
- **The server stays passive where it isn't invited.** The user-scope entry starts `mur mcp` in
  every repo the user opens. The server is on only while the repo says it uses Murmur (this working
  tree's `AGENTS.md` or `CLAUDE.md` has the marker, or the clone's remote-tracking default branch,
  `refs/remotes/<remote>/HEAD`, does) and this user has joined in this clone, checked every 2
  seconds. The working tree and the consent are read from files; the remote-tracking branch, which
  needs git and is read only in a joined clone whose working tree lacks the marker, at most every 20
  seconds. It counts so that a worktree on a branch made before `init` pushed isn't left off until
  it merges the default branch (#119). Off, it advertises a tools capability with no tools and no
  instructions, contacts no daemon, and answers a call that arrives anyway with why it is off. A
  join turns it on and lists the tools through `tools/list_changed`; a leave removes them. MCP
  instructions are fixed at initialize, so a server that started off never has them: the pages the
  section in `AGENTS.md`
  and `CLAUDE.md` links to carry that guidance. Nothing relies on the server alone, and the checks differ by
  what the thing is for. The server asks again before each registration with the daemon, before
  starting one, and before forwarding a tool call, since its 2-second look can lag a leave. The
  daemon is per clone, not per working tree, so it needs only the consent: it checks at start
  and again after its first sync, which takes a while, on every request but `stop` and `ping` (which reveal
  nothing, and are how a leave finds it), again in a registration after it has resolved the branch,
  before each push of its own (presence, the names registry) and before each handoff of a message
  as a turn (the one thing it does to the user's other programs, so a message that comes in by sync
  after a leave waits in the inbox, and isn't counted as a failure), and once a second, when it stops
  itself if the user has left. The last two are what end a daemon that was still starting when
  `join --leave` ran and so had no `daemon.json` to be found by. `mur daemon` and `mur ui` refuse in
  a clone that hasn't joined (so a stale client or a script can't start one). `join --leave`
  forgets the consent before it stops anything, then waits for the daemon's lock to be free as long
  as a replacement does, 40 seconds from asking it to stop (20 until #91, which was shorter than a
  shutdown may take), saying so on standard error once it has waited 5, and reports it as a problem,
  not as stopped, if it isn't. What it can't close: a push already past its check when the leave
  lands completes, and a starter already past its own check (Windows starts the daemon through WMI,
  which is slow) can still launch `mur daemon`, which exits at its consent check before it opens any
  state or publishes. A repo without the marker, or one the user hasn't joined, gets no daemon, no
  `refs/murmur/*` and nothing published. A clone whose user joined but whose current branch lacks
  the marker keeps its daemon (it is the user's, for every worktree) and gets no MCP tools there.
- **Per user, in every account T3 runs.** T3 can start Claude Code with its own `CLAUDE_CONFIG_DIR`
  (a provider instance with a home path, as `claude_personal` on Tyler's laptop, 2026-10-29), and
  Codex with its own `CODEX_HOME`. Until 2026-11-01 `mur join` wrote only to the home of the agent
  that ran it, so a thread under Tyler's other account had no Murmur tools and nothing said why.
  Now it also reads T3's `userdata/settings.json` (in the first of T3's data directories that has
  one, found as for the database) and registers in the home of every provider instance that is on:
  `config.homePath`, else the same variable in the instance's environment, else the agent's
  default; `config.binaryPath` finds an agent that isn't on PATH (Muse, here). Which instances are
  on, and the defaults, follow T3's own code (`internal/join/t3homes.go` says how). Homes are
  deduplicated, the agent's default home is the same registration as before, an instance that is
  off is named under "Not done", and without T3 nothing changes. The file holds API keys, so join
  decodes only those fields, takes an environment value only by name and only if T3 doesn't mark it
  sensitive, and never prints the file.
- **A session that started outside the repo** (2026-11-08, #200). The MCP server picks its repo from
  the directory the agent started it in, so a session started in a parent folder, the home directory
  or a workspace holding several repos never got a name in the repo it then worked in. Now each
  server publishes a record of itself (its PID, its agent's PID, Claude Code's session ID, the
  clone it is in and whether it is on) in the user's config directory, `murmur/sessions/`, and
  `mur join` finds the record of the session that ran it: the agent's process is the server's
  parent and an ancestor of the command, the nearest such ancestor winning. Claude Code also puts its
  PID in `CLAUDE_PID`, but that is used only where the ancestors can't be read: an agent Claude Code
  starts inherits it, and would otherwise move Claude's session instead of its own. Join leaves a
  move there, which the server picks up on its next look for a join or a leave: it turns off where
  it was and on in the joined clone, with the joined directory as its working directory. A session
  is in one repo at a time (Tyler accepted either answer; one was chosen because the tools take no
  repo, and two meshes would need one on every call and every message), so a move takes it out of
  the repo it was in. A move doesn't stand in for consent: in a clone the user hasn't joined the
  server stays off. Where one agent process runs several sessions and nothing tells them apart
  (Codex's app server in T3), join moves none and says so; OpenCode's one server serves every
  session in its directory, so it can't be moved; it publishes a record marked fixed anyway, which
  stops the search up the process tree, so a join from an OpenCode that Claude Code started is
  refused rather than moving Claude's session. A move carries the agent
  process's start time and Claude session ID from the record, so a new process that took a dead
  agent's PID doesn't follow it; records and moves of exited processes are cleared whenever they are
  read. Moving ends the old registration's context, and a call still on its way to the old repo's
  daemon with it: the client sets a past deadline on the connection when the call's context ends,
  which stops a write or read already under way, and checks the context just before sending. The instructions an MCP server
  gives at initialize can't change afterwards, so a session that moves in has the repo's section
  and the tool descriptions, not them. The hook is per worktree, and runs in the directory the
  agent started in, so commits aren't credited to a session that moved in (`internal/here`).

## Reloading MCP servers

Measured 2026-10-29 on Claude Code 2.1.312, Codex CLI 0.164.0 and T3 Code, with Claude and Codex
threads. The question was what a session needs after `mur join`, since the agent that ran it started
before the server was registered.

| Client | A server registered after the session started | `tools/list_changed` from a running server | What loads it |
|---|---|---|---|
| Claude Code | Ignored | Honored | A restarted process |
| T3 Code, Claude | Ignored | Honored | Press Stop, then send any message: T3 restarts the Claude process |
| T3 Code, Codex | Ignored (2026-10-30: a new thread loads it from `config.toml`; an older one still lacks it on its next turn) | Ignored | A new thread |

That shaped the design. Because `list_changed` is honored where it matters, the server is
registered once per user and starts in every session as a passive server: after the first join, a
Claude Code session opened in any repo gets the tools the moment that repo is joined, with no
restart. A session that began before the registration needs the restart in the table, and `join`
says so for the agent that ran it (`CLAUDECODE` and `T3CODE_CLI_PATH` in its environment): in T3,
Stop and a message; in a terminal Claude Code, exit and `claude --continue`; in Codex, a new
thread. When the registration was already there, the Claude Code tools appear in a few seconds.
Not verified: that `claude --continue` loads the server (inferred from Claude reading its settings
at start), that Codex honors `default_tools_approval_mode = "approve"` in a live thread (the
config is valid and `codex mcp get murmur` shows it), and which variables Codex sets for the
commands it runs (`CODEX_THREAD_ID`, `CODEX_SANDBOX`, `CODEX_CI` are assumed; with none, `join`'s
advice covers every client).

More on Codex in T3, from a Codex agent in a T3 thread (Codex 0.164.0, 2026-10-30). T3's Codex
provider loads a stdio server from `~/.codex/config.toml` in a newly created thread: its echo tool
worked, with no approval prompt in full-access mode. A thread that existed before the server was
added still lacks it on its next turn. Codex's app-server has a `config/mcpServer/reload` request
that refreshes a running thread without a restart, but the installed T3 never sends it. So `join`
tells a Codex thread in T3 (Codex's variables plus `T3CODE_CLI_PATH`) to "open a new thread in this
project to talk to other agents through Murmur". Still being tested at the time of writing: whether
a project-level Codex config is read, and approval policy outside full-access mode (findings go to
#55 as comments).
