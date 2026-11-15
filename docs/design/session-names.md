# Session names

## Session names

Tyler's decisions, 2026-10-28 (#20). Every session has a name of two words, an adjective and a
noun (`quiet-otter`), unique in the repo and never reused, not even after its session ends. The
name is the address: `send` takes it, `who` shows it, and notices and `inbox` name the sender.
`device/session` still works. It is what a message carries on the bus, so the event format didn't
change and a daemon that knows no names routes by it all the same.

- **Taking a name.** A daemon picks a random name nobody has, commits it on `refs/murmur/names` (one
  commit per record: the name, the device, and Murmur's session ID, or no session for a name set
  aside, below), and pushes with the lease on the registry tip it last read. The name is the
  device's only if that push lands. If another device got in first the push is refused; the
  daemon fetches, reads what it missed, and picks again. It stops after 5 refusals in a row and
  backs off, up to 5 minutes. What it has to record at one moment goes in one push, as a chain of
  commits, one record each.
- **Names set aside (#141, 2026-11-01).** A name took 2–16 s to land after its session started,
  with the network up (a push to GitHub, and a retry when GitHub refused it), and a session showed
  as pending all that time; in an outage it waited for the network. So a daemon keeps 10 names
  (`NamePool`; 3 in v0.2.2, 10 since #145 so a burst of new sessions or a long outage doesn't
  empty it) registered ahead for sessions it has yet to start: a commit with the name and the
  device and no session. A session with no name takes the oldest at once, without the remote, and
  it is the session's name for good, as any name is. The daemon then records the assignment, the
  name, the device and the session, as it records any, and sets another name aside in the same
  push. The names are unique because they were registered before anyone used them.
  - *How others read one.* A daemon that knows reservations records the device; a later
    assignment of the name counts only from that device, and a reservation of a name already in
    use counts for nothing, so a reader of the whole history and one that reads only what is new
    agree. A repair's new root sets the outstanding names aside again, each for its device, ahead
    of its snapshot of the rest. A taken name is its session's address at once, on its own
    device, even before the assignment lands. A daemon that predates them reads a
    commit with no session as a name in use by a session it doesn't know, so it never hands the
    name out either. Until the assignment lands, other devices find the session by the name in
    its device's presence, as they find any live session.
  - *Sending to one nobody has taken* is an error, but not "no session is named": it says that
    device has set the name aside for a session it has yet to start. A daemon that predates
    reservations says no session has it.
  - *A device that never comes back* leaves its names set aside for good. That burns at most 10
    names a device, out of 1.25 million, and a name is never reused anyway. A daemon that loses its
    state file burns its pool the same way.
  - *The first start* with no network has no pool: its sessions are pending, at
    `device/session`, as below, until the remote is reachable.
- **A name follows the client's session ID.** The key is the full `ClaudeSession` the MCP server
  gives when it has one (Claude Code's `CLAUDE_CODE_SESSION_ID`), else the Codex thread ID (see
  below), else the ID Murmur gave the session.
  The same key keeps its name across the MCP server restarting, the daemon restarting and a
  resume; different keys never share one. A daemon keeps the key-to-name map in its own state
  file, so the client's ID is never published. A resume of the same client session on another
  device is a new session there, with a new name. Two live sessions with one key, such as two MCP
  servers in one conversation, would share a name, so the later is a session of its own. A key
  that comes back under another ID while its old session is still in its grace takes the name
  with it, and presence shows the name on the live session only, so a message to it isn't left
  in an inbox about to stop (2026-10-29).
- **A Codex session outside T3 follows its Codex thread (#36, 2026-10-30).** Codex's MCP server
  learns the thread ID at its first tool call (`_meta.threadId`) and reports it as the route's
  `Native`; the key is then `codex:<threadId>`, after `t3:` and `claude:` in the order of
  preference. Codex keeps the thread ID across resume and compaction and gives a new one on `/new`,
  `/clear` and a fork, which is the life a name should have. So a Codex CLI session whose MCP
  server restarts is a new Murmur session with a new name until its first tool call, and then takes
  the thread's old name back, as a T3 thread does. A session that already has a name when it learns
  its thread keeps it for the thread, if the thread has had none. Inside T3 the `t3:` key still wins
  once the T3 thread is known.
- **Never reused, whatever happens to the registry.** A daemon keeps every name it has ever seen,
  in the registry's whole history or in any device's presence, and the set never shrinks, so a
  registry that is deleted, overwritten or force-pushed frees nothing. When the tip isn't a
  readable commit, or its history no longer holds what was read before, the next commit is a new
  root holding a snapshot of every name the daemon has seen, leased on the tip it found. A daemon
  writes that on noticing the damage, not when it next needs a name, so that one joining later
  learns what was taken. A listing that shows no registry, when the daemon has read one, is
  checked again before it counts as a deletion: a sync lists the remote before it takes the lock
  that records the daemon's own pushes, so it can predate the daemon's first name (#80,
  2026-10-30; the first version took it for a deletion and replaced the registry's history with a
  snapshot).
  - *What one record can add (2026-11-09).* Since the set never shrinks, a reader bounds what it
    takes from a single record. A snapshot counts only on the root of a history read from its start,
    and only if that history has one root: the one place Murmur writes it, as a repair's history
    never descends from what was read, and Murmur writes no merges. One appended is logged and
    passed over. A merge that brings in another root, by either parent, is damage, as a rewrite is:
    no snapshot in it counts, which would hide a real repair's names from a reader new to the
    registry, so every reader that sees it logs it and reads the registry from the start, and one
    that knows a name missing from it writes a repair, which replaces the merge. The names in the
    merged-in root's snapshot never counted, so the repair doesn't bring them back. A presence
    listing more than 200 sessions (`bus.MaxSessions`), far beyond what one device runs, is read as
    unreadable, whole, so none of its names counts. A daemon's own presence keeps to the cap: past
    it, it lists the most recently active sessions, and logs how many it left out. And once 1,000
    random picks have all been in use, a daemon goes through the lists from a random place for a
    name nobody has. Push access can still mark names used, more slowly: a registry rewritten from a
    root of its own, which readers log and repair, and 200 names per presence. So can a daemon from
    before this, which took a snapshot from any commit, until the team has upgraded: its next repair
    writes what it took into a real root.
- **Without the remote.** A session that finds no name set aside and can't get one works as
  `device/session`, with its name shown as pending, and the daemon keeps trying in the
  background, and at once when it can reach the remote again.
- **Sending to a name.** A name is looked up in the sessions on this device, then in this device's
  record of its last holder (see A name held by another session), then in other devices'
  presence, then in the registry, so a message to a session that has stopped is kept for it like
  any other. A name nobody has is an error that says so. A device ID shaped like a name is read as
  a device if no session has that name.
- **A name a session has left.** A session that followed Claude Code to another conversation
  (#9) isn't called its old name any more, but the registry still has that name down for it, as
  its address is its Murmur ID, which doesn't change. A send to the old name reaches the session,
  and says so: the name was its before it moved, and it is called something else now. The
  alternative was an error saying the conversation ended; that would drop messages to a session
  that is alive and could read them, and the sender could do nothing with the error but look up
  the new name that the note gives it (2026-10-29).
- **A name held by another session (#37, #46).** A `/resume` back to an earlier conversation can put
  its name on a session whose Murmur ID isn't the one the registry has the name down for, and the
  first session is then retired. A daemon records in its state file the last session to hold each
  name and the last name each of its sessions held. Mail for a name goes only to a session whose
  last name is still that name, but for the registry's own session (below). A message for a session
  of this device that isn't running goes to the session that holds the name it last had, if that one
  is registered or stopped within `StoppedInbox` and the name is still the last it took: whether it
  is sent by name or by `device/session`. It is one hop. A holder that has moved to another
  conversation since, running or not, gets nothing for the name it left; the message stays in the
  stopped session's kept inbox (2026-10-29). Read from a backlog after a restart, a forwarded
  message is the holder's if it is new enough for either session, so what the stopped session's kept
  inbox would have caught up on still arrives when the holder registered first. And a session that
  takes a name, by registering, by a takeover in its grace or by following a hook, takes the inboxes
  kept for the sessions, stopped within `StoppedInbox`, whose last name it was. A conversation comes
  back as whichever session takes its name next: the registry's own ID when it is resumed from the
  command line, since the server's ID is derived from the conversation's; the last holder, if its
  server restarts; or a third session a hook moves to it. A message to the name waits for any of
  them (2026-10-29).
  - *Sent by name on the name's own device,* a message goes to the name's last holder there,
    running or not, if the name is still the last that session took. Otherwise the registry's
    address goes on the bus as it would from any device, and is forwarded where it is queued, so no
    message is forwarded twice.
  - *The registry's own session.* A message is addressed on the bus, and the address for a name is
    the session the registry has it down for. Once that session has left the name, a message to the
    name reaches it, as A name a session has left says: running, under its new name; stopped, in
    its kept inbox, or forwarded by the name it last had to the session holding that one. The name
    sent to isn't on message events, so the receiving daemon can't tell; putting it there is a bus format
    change, Tyler's to decide (2026-10-29).
- **V1 daemons.** They don't read the registry, and their presence has no names. Their sessions
  show without one and are reached at `device/session`, and they reach sessions named here the
  same way.

## When a session stays the same session

A session's name (#20) lasts as long as its client treats it as the same conversation, so it
follows the client's own ID: a resume keeps the name, and `/clear` or a fork gets a new one
(Tyler, 2026-10-28). Each client keeps and changes that ID at different moments. Researched
2026-10-28 on Claude Code 2.1.310 and 2.1.312, T3 Code 0.1.2 and Codex CLI 0.164.0; the
evidence is in #19.

| Client | The ID | Kept across | New on | Where Murmur can read it |
|---|---|---|---|---|
| Claude Code | session ID | compaction, every kind of resume, a killed process resumed | `/clear`, `--fork-session`, `/branch`, switching with `/resume` | A hook's `session_id` on stdin, always current |
| T3 Code | thread ID | everything, for good | a fork, which is a new thread | Nowhere, as shipped |
| Codex | `threadId` | compaction, every kind of resume | `/new`, `/clear`, a fork | `_meta.threadId` on every MCP `tools/call`, and a hook's `session_id` |

- **Claude Code's MCP servers can't be trusted with the ID.** `CLAUDE_CODE_SESSION_ID` is set
  once per MCP server process and never updates, so it goes stale after `/clear`, `/branch` or
  switching with `/resume`. Under `claude -p --continue`, or when `claude` is opened and then
  `/resume`d, it is wrong from the start. Hooks always see the live ID, and they get
  `CLAUDE_PID`, which is the MCP server's parent process. So the daemon learns the live ID from
  a hook and matches it to a connected session by that PID (#9).
- **How the daemon follows the live ID (#9, built 2026-10-29).** The MCP server's hello carries
  its parent PID and the hook's request carries `CLAUDE_PID`. On a hook request the sessions
  whose PID is the hook's are the hook's, and each of them not yet on the hook's Claude ID moves
  to it. The key follows the ID, so the session takes the name that key has had, or a new name
  taken as any is (#20); it keeps its Murmur ID, its inbox and its place on the bus, and nothing is
  written there. Presence carries the new name, and the daemon's log says each switch in names
  only, since Claude IDs are never published. Two servers in one Claude Code process both move,
  and still don't share a name. A PID moves only the sessions that registered with it, so a hook
  takes no session of another process. Where no session has the hook's PID (the hook has none,
  the server gave none, a wrapper stands between Claude Code and the server), the sessions with
  the hook's Claude ID are the hook's, as before PIDs were known; a hook that matches neither
  gets nothing.
  - *Reconnecting.* Every response carries the Claude ID the daemon holds for the calling
    session, and the server sends it in later hellos instead of its environment's. A server that
    registers again from the same PID, within the grace, keeps the daemon's ID whatever the hello
    says, whether a hook or a hello gave the daemon that ID; a server of another PID is believed.
    A daemon that restarts between
    a switch and the server's next call to it can still bring the stale ID back, until the next
    hook corrects it.
  - *The parent PID.* `mur join` registers `<absolute path of mur> mcp` with Claude Code (see
    Joining a repo), so Claude Code starts it itself and the server's parent is the Claude Code process,
    unless `mur` is a shell or batch shim, or the user wraps it. Seen 2026-10-29 on Windows, with a plain
    Python server rather than Murmur: its parent was the `claude.exe` that `CLAUDE_PID` named, and a
    `cmd.exe` stood between them only where the user's own config said `cmd /c`. A wrapper means
    no PID match, so that session falls back to its Claude ID and doesn't follow.
  - *When it happens.* `mur join` installs the hook for `PreToolUse`, `PostToolUse`,
    `PostToolUseFailure` and `UserPromptSubmit`, not `SessionStart`, so a session moves at the
    first tool call or prompt after the switch, not at the switch. Until then it shows its old name, and what is sent to that reaches it.
  - *Subagents.* A subagent's hooks carry its parent session's `session_id` and `CLAUDE_PID`,
    with `agent_id` set (seen on 2.1.312, 2026-10-29). One started before a `/clear` may still
    report the conversation the session left (inferred, not seen; #231), so a subagent's hooks find
    the session by its process and never move it.
- **Under T3 the session is the thread, not the provider's session under it.** The Claude
  session ID under one thread changes when the user switches Claude accounts, and has gone
  A → B → A. T3 gives MCP servers and hooks no way to learn the thread ID. Tyler decided on
  2026-10-28 not to change T3's source, so Murmur looks up the provider's session in T3's database
  (#56, 2026-10-29): "Delivering a message as a turn", below.
- **Codex's MCP servers get a bare environment,** about 20 Windows core variables and no
  `CODEX_*` or `T3CODE_CLI_PATH` (unless the Codex config forwards them, as `join` does for
  `T3CODE_CLI_PATH` and `CODEX_HOME`), so the server learns its
  thread at its first tool call. Hooks under Codex inherit
  the `CLAUDE_CODE_SESSION_ID` of any Claude Code session that started Codex, so that variable
  doesn't prove a hook runs under Claude Code.
- **Two live processes can hold one ID.** Claude Code lets a session be resumed twice at once.
