# Handing a message to each client

- **Finding the thread (#56).** T3 gives an MCP server or a hook no thread ID, so the MCP server
  looks up the provider's own session in T3's database: `statev2.sqlite` in T3's data directory
  (`T3CODE_HOME`, or the directory the CLI's `bin` is in, or `~/.t3`), table
  `orchestration_v2_projection_provider_threads`, where `payload_json.nativeThreadRef.nativeId`
  is the Claude Code session ID or the Codex thread ID, next to the `thread_id` column (seen on
  T3 0.1.x, 2026-10-29). The ID comes from the hook or `CLAUDE_CODE_SESSION_ID` for Claude Code
  and from `_meta.threadId` of a tool call for Codex (every tool takes it); the database decides
  which is a thread. The lookup is set up with the session's link, where the link is made, so a
  server turned off by a leave and on again by a join finds its thread as the first link did. The
  lookup only reads: SQLite's read-only mode (never immutable, which would ignore the write-ahead
  log and miss the newest threads), at most 1.5 s on a lock and 4 s in all, and any trouble leaves
  the session with the keys it had before. The reader is `modernc.org/sqlite`, SQLite translated
  to Go, so `mur` stays one binary built without a C compiler for every platform. The cost is
  size: with everything else in this change, `t4.exe` for Windows grew from 17.7 MB to 23.8 MB
  (12.3 MB to 16.2 MB built with `-s -w`) against `main` on 2026-10-29, almost all of it SQLite.
  `ncruces/go-sqlite3`, SQLite as WebAssembly, added more (10.5 MB on a probe).
- **The session is its thread.** A session in T3 is named after its thread, not after the
  provider's session under it, so its name stays through account switches, `/clear` and restarts
  (see "Under T3", above). The daemon keeps the thread, the CLI's path and T3's data directory
  per session in `.git/murmur/state.json`, so a message for a session whose MCP server T3 has
  stopped (after 30 minutes idle, for Claude Code) still finds its thread. The thread, the messaging
  token and the socket's address stay on the device: never on the bus, the page or the log. When
  T3 restarts a thread's MCP server, or the connection is made again, the new session has the
  thread and the old one, in its grace or stopped, loses its route: a message for everyone is
  handed to the thread once, and a session's own broadcast doesn't come back to it as a turn.
  What the old session had unread goes to the new one, which now has the way in, in the order the
  messages arrived, behind any that was already on its way in; it leaves behind what the new
  session sent itself and what came while it was running and was for it too, as it had its own copy
  of those, which it may have read. A message for everyone that comes in the second or so between
  a new session registering and finding its thread is handed to the thread by the old session,
  which still has the route, and the new session's copy is not handed over again when the route
  moves to it (#70, 2026-11-11): the daemon remembers, by event ID, what it handed to each thread
  for the last 30 minutes (up to 200 messages a thread, in memory only), and takes another copy of
  one out of the inbox of any session of that thread, as the thread has it. A message for the new
  session alone is another event, and goes in. Not covered: a new session that registers before the
  daemon has seen the old one's connection close. The old one, still running then, keeps the route
  through its grace, both copies can be on their way in at once, and the thread may see a message
  for everyone twice (#99). Handing the thread over when the drop is seen was tried
  (2026-10-30) and dropped: in three review rounds each version could strand a message to the old
  session's name, where a repeat is all that happens now.
  A session that found its thread keeps its name under the Claude session ID too, so the ID does
  not bring that name to a second process of the same Claude session; it is a session of its own.
- **Codex under T3** gets the thread from `_meta.threadId`, which every tool call carries (all five
  tools note it, `lookup` too), but its MCP servers start with a bare environment, `T3CODE_CLI_PATH`
  not among its variables, so Codex's config must forward it: `env_vars = ["T3CODE_CLI_PATH"]` under
  `[mcp_servers.murmur]` (worked with Codex 0.164.0 under T3 0.1.2, 2026-10-29). `mur join` writes
  it with `CODEX_HOME` (Session names, #88), and adds a name missing from a list the user already
  has there without touching their other entries; a list over several lines, or of anything but
  names, it leaves alone and says what to add by hand. Codex leaves out a name its own environment
  doesn't have, without a warning (`create_env_for_mcp_server` in `codex-rs/rmcp-client`, read
  2026-11-09), so a session in `~/.codex` starts its server as before.
  Without it the server doesn't know it is in T3, and a Codex session gets notices only. Codex
  reads a project's `.codex/config.toml` only once the project is trusted.
- **The messaging socket** is for Claude Code outside T3. Claude Code sets
  `CLAUDE_CODE_MESSAGING_SOCKET` (a unix socket, a named pipe on Windows) and
  `CLAUDE_CODE_MESSAGING_TOKEN` in every child process, and the MCP server passes both to its
  daemon with its hello. The protocol, as seen on 2.1.312 on 2026-10-29: JSON lines, an auth line
  `{"type":"auth","token":...}`, then `{"type":"user","message":{"role":"user","content":...}}`.
  The socket answers nothing, so Murmur knows a message was written, not that the session took it.
  The token dies with the session's process, so the socket is used only while the session's MCP
  server is connected. A session inside T3 is never written to this way: the socket there is T3's.
  Claude Code wraps what arrives on the socket in a note of its own that says the message came
  from another Claude session, not the user (seen 2026-10-29). It suits Murmur's framing, but it
  costs: on Claude Code 2.1.316 the note is a fixed paragraph of about 540 characters (about 135
  tokens), added to every message, after the "Reply with Murmur's `send`…" line. It repeats what
  Murmur's own header already says, and a person writing from the page isn't a Claude session at
  all. Murmur can't remove it: the socket is the only way to start a turn in an idle Claude Code
  session outside T3. anthropics/claude-code#94383 asks for it to be shortened or made optional,
  and Murmur's case is added there (Tyler, 2026-11-13). A turn delivered through T3 carries no
  such note.
- **Pi's extension (2026-11-09, #205).** Pi has no hooks of Claude Code's kind, so the extension
  `mur join` writes (`extensions/murmur.ts`) brings Pi #202's delivery itself, with nothing new in
  the daemon but a way for the hook to name a Pi session: by its file, `Native` in the hook's
  request, as the session's MCP server registered with it. It runs `mur hook --agent pi` at Claude
  Code's moments: as a run starts (`before_agent_start`, a prompt), after each tool call
  (`tool_result`; the notice goes in as a message `deliverAs: "steer"`, which Pi puts after the tool
  calls and before the next model request), and as the work ends (`agent_before_settle`, where a
  `custom_message` entry and `continue: true` keep it going, as blocking Stop does). Pi, unlike
  Claude Code, says when the work ended without a chance to go on: an error is the boundary's
  `outcome`, and Escape skips `agent_before_settle` and sets `agent_settled`'s `aborted`. The hook's
  Stop then says it was interrupted, and the daemon takes the session for idle and hands what is
  unread over as a turn, as for a T3 turn the user stopped. It opens a socket in the session that
  takes Claude Code's messaging-socket lines, and gives its address and token to the MCP server in
  its environment as Claude Code does, so the daemon hands an idle Pi session a turn the way it
  hands Claude Code one; Pi takes it with `triggerTurn`, and with `deliverAs: "followUp"` a busy one
  takes it when it finishes, not steered in. What Murmur says is a custom message, shown in the
  session and read in the user's role, never `sendUserMessage`, which Pi treats as the user's
  typing. The extension is in every Pi session, so it runs the hook and opens the socket only in a
  clone the user joined (its `joined.json`): elsewhere a tool call waits on no process of
  Murmur's (about 0.1 s each, Opus's review). It looks for the consent as the session starts, then
  each second and at each prompt and tool call until it is there, a few stats each time: the user
  usually joins from inside a running session, which would otherwise get nothing until its next
  session (the re-review's R1, reproduced with a real Pi). The socket's address and token are
  chosen at the start and given to the MCP server, which registers only once the clone is joined;
  a message that comes in the second before the socket opens is tried again after 2 s, and failing
  that waits for the hook. An update doesn't run `mur
  join` again, so Pi's MCP server rewrites an extension join wrote for its own binary when the
  text is an older binary's; Pi reads extensions as a session starts, so the first session after
  an update still has the old one. Seen with Pi 1.5.0 against a stand-in model, in RPC mode and in
  its terminal UI (driven in a pty by Opus's review, then here): a notice after a tool call, the
  message whole at the end of the work, a turn when idle, a turn right after an abort, a new socket
  and session after `/new`, no socket in a repo not joined, and an old extension rewritten.
  *Pi in T3 Code (2026-11-11, #462)* gets the same, with nothing added for it. T3 starts Pi as
  `pi --mode rpc`, which loads the extension, and Pi gives its MCP servers its own environment,
  T3's `T3CODE_CLI_PATH` in it; T3 records the session's file as the thread's native ID, the same
  path the extension gives the server, so the session finds its thread. Seen with Pi 0.99.0 on
  Windows, started as T3 starts it, against a stand-in for the rest of T3 (the rows T3 writes to
  its database, and a `t3` that hands what it is given to Pi as a prompt): a notice after the tool
  call, the message whole at the end of the work, and an idle message as one turn through
  `t3 thread send`, each once. The same run with no T3 in the environment took the idle turn
  through the named pipe, the first time that was tried. Not seen in T3 itself: Pi was switched
  off in T3's settings on the machine. T3's Pi adapter has no case for Pi's `custom_message`
  (T3 0.1.x, read 2026-11-11), so the notice and the message at the end of the work reach the
  model and likely don't show in the thread; a turn through T3 shows as any message does.
  Not covered: Pi's commits aren't credited, as no call is bracketed, and a session
  without a file (`--no-session`) gets turns and no notices.
- **The client's command line (2026-11-11, #135)** is for Codex and OpenCode 2 outside T3, which
  until then got a message only through `inbox` (and Codex a notice, where its hook runs). Each
  has a command that does what `t3 thread send` does, so the daemon runs it:
  - *Codex:* `codex queue --thread <thread> --message <text>`, with `CODEX_HOME` set to the
    session's. The message goes to the thread's queue, and a thread that is open takes it as its
    next turn. Seen on Codex 0.167.0 (2026-11-11): a terminal session that sat idle showed the
    message as a prompt and ran the turn about 3 s later. Codex's hook runs there, so the daemon
    knows when the session is at work, and a busy one gets the notice and its turn's end as before.
  - *OpenCode 2:* `opencode api session.prompt --param sessionID=<session>` with the body
    `{"text": ..., "delivery": "queue"}`, which asks the background server that every OpenCode 2
    client on the machine shares. `"queue"` waits behind a running turn (the default, `"steer"`,
    goes into it). When this was built OpenCode had no hook of Murmur's, so the daemon couldn't
    tell a busy session and handed it the message either way; since 2026-11-12 its plugin runs the
    hook (#431), and a busy session gets the notice and its turn's end, as Codex does. Seen on OpenCode 2.2.0
    the same day: the turn ran, and the terminal UI open on the session showed the message and the
    answer. `session.synthetic` starts a turn too, and the terminal UI then shows the answer and
    not what was asked, so it isn't used.
  - *Which program.* The session's MCP server names the program that started it (its parent
    process), if that is a `codex` or an `opencode`, and the daemon runs that one. Neither
    command reads the text from standard input or a file, so it goes on the command line, and
    the name on `PATH` won't do: on Windows it is npm's `.cmd` shim, which can't carry a newline,
    and it may be another install. On Windows a message whose command line would pass 32,767
    characters (a body near its 32 KiB limit) isn't handed over, and stays in the inbox.
  - *Only while the session's MCP server is connected*, as for the socket. A Codex thread nobody
    has open keeps a queued message until it is next resumed, and by then it has left Murmur's
    inbox. Connected doesn't mean watched: Codex's background app server kept a thread, and its
    MCP server, after the terminal on it closed, and 40 minutes later a queued message ran a turn
    there with no window open (seen 2026-11-11; the answer was in the thread). The page's wake
    setting is what limits that, as it does for an idle T3 thread. For OpenCode this is stricter than it need be: its server would run the turn with no
    window open, but a session of the shared server is dropped after 30 minutes without a tool
    call, and a message after that waits in the inbox.
  - *OpenCode's shared server only.* The server names OpenCode's program only when the process
    that started it is the one registered in `service.json` in OpenCode's state directory
    (`~/.local/state/opencode`, `"pid"`). A session of a private server (`--standalone`,
    `--server`) is left to the inbox: `opencode api` would start the shared server to ask it
    about a session it doesn't have. So is OpenCode 1, which has no `api` command; its terminal UI
    serves HTTP on a port of its own that nothing tells Murmur. If a later OpenCode keeps the
    registration elsewhere, its sessions go back to the inbox, silently.
  - *A failure* is handled as `t3 thread send`'s is: tried once more only if the command never
    started, and otherwise left to the inbox and the hook's notice. A Codex from before `queue`
    fails every time, and the daemon's log says so once per session.
  - *When it is a turn.* A Codex session has no way in until it has called one of Murmur's
    tools, which is how its server learns the thread. And the wake setting counts for these as
    for every session: by default one idle more than a few minutes isn't woken. For OpenCode,
    with no hook, "last at work" is its last call to a Murmur tool, and with no hook there is no
    notice either, so a message that comes after that waits in the inbox unannounced.
  - *On the command line.* The text is an argument of the command for the second or so it runs,
    where the machine's other users can read it (`/proc/<pid>/cmdline` on Linux) and where
    process auditing records it. Every other way in passes it over a pipe or a socket. OpenCode's
    HTTP API (the `url` and `password` in `service.json`) and Codex's app server
    (`thread/queue/add`) would avoid that, and the Windows length limit; both are more of each
    client's insides than the documented command.
  - *Whoever runs the client.* The way in is there for any session whose MCP server a `codex`
    started, not only a terminal's: Codex's desktop app, `codex exec`, a thread another tool
    drives through `codex app-server` (Claude Code's codex-sub plugin does). An OpenCode child
    session likewise. Each can be handed a turn its driver didn't start, within the wake
    setting; before, they had the inbox only. Not tried. A subagent Codex itself spawned was
    one of them until #454: its first Murmur tool call now takes it off the mesh (see "The
    subagents Codex spawns are delegates too").
  - *Not covered (#446).* Muse and Grok outside T3 (neither was installed where this was built). A Codex
    session run by T3 whose config doesn't forward `T3CODE_CLI_PATH` reads as one outside T3, and
    gets `codex queue` (not tried). OpenCode's one MCP process per directory has the environment
    of whoever started the shared server, so a T3 thread on a server a terminal started reads as
    outside T3, and a terminal session on one T3 started as inside (not tried). Whether Codex's desktop app shows a queued message as the
    terminal does wasn't looked at. macOS and Linux were not tried with the real programs, only
    Windows.
- **A thread that is idle (#87).** T3 stops an idle thread's provider process, and with it Murmur's
  MCP server, after about 30 minutes (seen live on 2026-10-30: both threads' sessions stopped at
  03:30, half an hour after their last turn), so the session of a thread that is idle is the normal
  case, and the thread most worth waking. Its route is kept in `state.json` (`T3Routes`) for as long
  as its inbox is, 24 hours from the stop, and the daemon stays up for as long as it holds one: a
  daemon that exited 10 minutes after the last session, as it did first, left a message to such a
  thread waiting for the next time anyone used the clone, by when it was past the 15 minutes a
  message may wake a session and arrived as a notice. A route counts only if the page's wake
  setting would let a message through to the thread. With no session live and no page open the
  daemon looks at the remote every 30 s (see "Speed"): about 2,900 `ls-remote` calls in a day of
  waiting, and a sleeping thread wakes up to 30 s later than a working one would. It still pushes its presence every 2 minutes (about 720 pushes a day), which tells others
  the device is there. Claude Code outside T3 has nothing like it: its messaging socket dies with
  the session. `mur join --leave` and `mur stop` end the daemon at once.
