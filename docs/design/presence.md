# Who is on, and what they have done

## Who is on, and what they are doing

Tyler's decisions, 2026-10-29, built 2026-10-30 (#66). With many agents on, `who` had a line for
every session ever seen, the stale ones above the live ones, and a context only where an agent
had called `status`: in a live test it listed three devices left from earlier tests above the two
agents that mattered.

- **A cap.** `who` lists sessions most recently active first, at most 20 in all (`view.WhoLimit`),
  then says "and N more, less recently active; use lookup with a session's name to see one". Sessions
  are ranked across devices, and shown under their devices; a session whose device doesn't say when it
  was last active ranks by when it started. The caller's own session and device are always shown,
  and count toward the 20. A device that has gone stale is one line (its name, who it belongs to,
  when it was last seen, how many sessions it has) and its sessions are neither listed nor counted
  among the "more". The cap is in the text `who` renders, so the CLI and the MCP tool share it; the
  daemon's reply still holds every session.
- **Held to what a device can say (2026-11-09).** Anyone who can push can make any number of device
  refs, and date a presence as they like. So `who` lists at most 5 devices with no session listed,
  stale or with none (`view.WhoQuiet`), and counts the rest in one line. Stale devices that still
  have sessions come first, as their line is the one an agent acts on, then the most recently seen:
  a device that keeps pushing is always seen recently.
  And another device's session start is dated as its last activity is, on the reader's clock and
  no later than the presence that lists it: a start dated ahead had ranked its sessions above every
  other in every agent's `who`.
- **Last active on every line**, as `active 2m ago` after the agent and branch (`lookup` keeps its
  own, longer form). A session on a daemon from before #21 has none, and its line says when it
  started instead.
- **The conversation's title is the default status line.** An agent that has set no status shows
  the title of its conversation in `who`, `lookup` and the page; a `status` it sets always wins,
  and setting it to nothing brings the title back. The title is read from the client's own state,
  which belongs to the client, is versioned by it and is open for writing in it, so every reader
  opens it read-only, waits no more than a moment for a lock, and treats a missing file, table or
  column, a lock or a shape it doesn't know as a read that failed, which keeps the title the session
  had and costs nothing else. Where each client keeps it (seen 2026-10-30):

  | Session | Title | Not used |
  |---|---|---|
  | In T3 (its thread found, see Delivering a message as a turn) | `orchestration_v2_projection_threads.title` for the thread, in T3's `statev2.sqlite` | The client's own name for the conversation: T3 doesn't copy its title there, and the two would take turns |
  | Codex outside T3 | The thread's explicit name: `threads.name` in `state_N.sqlite` (the highest N) of `$CODEX_HOME` as the session's MCP server has it (Codex starts it with a bare environment, so `join` names `CODEX_HOME` in `env_vars`, #88), else `~/.codex`, which the server names too, rather than leave the daemon its own `CODEX_HOME`, the home of whichever session started it; a server from before #88 names no home, and gets the daemon's `CODEX_HOME`, else `~/.codex`, as before; failing that `thread_name` in `session_index.jsonl`, the last entry for the thread | `threads.title` and `threads.preview`, which hold the opening message |
  | Claude Code outside T3 | The last `custom-title` line in the session's transcript (the name the user gave it), else the last `ai-title` line (the one Claude Code made up) | The first prompt: a session with neither has no title |

  The transcript is the hook's `transcript_path`, which the hook sends with its notices request;
  Claude Code rewrites the title lines near the end of the file, so the last 128 KB is read (2.1.312;
  in the 300 largest transcripts on the laptop, 2026-10-30, the last `ai-title` was within 31 KB of the
  end). A new path, as after `/clear`, is a new conversation and drops the old title. Codex puts
  its thread ID in the `_meta` of every tool call, and the MCP server hands it to the daemon in the
  route (protocol 2) even outside T3.
- **It goes out as presence.** The daemon puts the title in the session's `doing`, the field a
  status goes in, so the bus format is unchanged and an older daemon shows the title as it shows a
  status. A reader can't tell the two apart. **A conversation's title is therefore shown to every
  teammate who can read the bus, unless the agent sets a status**; it is cut to the status line's 120
  characters, and one that `scrub` would stop in a status is not shown.
- **When it is read.** A daemon goes through its sessions every 5 seconds (`Options.TitleEvery`) and
  reads each source once, outside its lock, so no database is read more often than that. A session
  with a status is not read. A changed title causes a presence write, as a changed status does, so it
  shows on other devices within seconds; a title that hasn't changed writes nothing.

## What a session has done

Added 2026-10-29 (#21). `lookup` (an MCP tool; it has no command, like the other messaging tools)
gives an agent what `who` doesn't: the branches another session has committed to, when it was last
active, whether a message would reach it, and its latest messages. A session on this device also gives its worktree folder,
the commits it made and the client's session ID, which stay on this device. Git doesn't record
which session made a commit, so the daemon watches tool calls for them.

- **Bracketing a call.** `mur join` installs the hook on `PreToolUse`, `PostToolUse` and
  `PostToolUseFailure` as well as `UserPromptSubmit` and `Stop` (the end of a turn, #202), with
  matcher `*` on the three tool events, and run again adds what an older join lacks. A repo set up by an `init` from before #55 had the hook
  in its committed `.claude/settings.json`; `mur init` removes it there, and the user's `mur join`
  installs it in Claude Code's own settings (#422). On `PreToolUse` the daemon snapshots the worktree
  of the hook's working directory (`attrib.Head`: HEAD, branch, every ref tip and every worktree's detached HEAD). On the call's end it
  snapshots again and credits `attrib.NewCommits` between the two to the sessions the hook speaks
  for, the ones its notices go to. A commit made in that worktree during the call by a human or
  another session is credited too: git can't say who made it, and this was accepted. Package
  `attrib` decides which commits count.
- **Pairing.** The two ends of a call share a `tool_use_id`, as does a call that fails, which fires
  `PostToolUseFailure` instead of `PostToolUse` (seen on Claude Code 2.1.312, 2026-10-29); the hook
  closes the call on either, so `git commit && git push` with a failed push is still credited. The
  key is the Claude session ID and the `tool_use_id`, since calls run in parallel. A snapshot is
  dropped after an hour, and the oldest goes once 128 are held, so a call whose end never comes (it
  was cancelled, the hook crashed) can't pile them up. A call with no snapshot credits nothing.
- **Which worktree.** The one the call started in, and the one it ended in if that is another
  worktree of the clone: the start's tips are the whole clone's, every worktree's detached HEAD
  among them, so they keep either worktree's history out (#60). A `cd` inside a Bash call leaves the hook's working directory where it
  was (seen on 2.1.312, 2026-10-29), so a call ends elsewhere only when Claude Code itself has
  moved, and what `cd ../other && git commit`, or `git -C ../other commit`, makes in another
  worktree isn't credited. The hook finds the clone from its working directory, and a call in
  another repository, or none, isn't watched. A subagent's hooks carry the parent's session ID,
  so its commits are credited to the parent.
- **What it costs.** A call that cannot run a command (`Read`, `Glob`, `Grep`, `Edit`, `Write`
  and the like, named in `internal/hook`) isn't bracketed. Any other tool is, MCP tools and
  subagents included. The start is one `git` process: the hook finds the clone from its files, and
  `attrib.Head` reads HEAD from the worktree's git directory and the other worktrees' HEAD files, and asks git only for the refs (a
  reftable repository, or one git might find differently, takes four). It can't be made to run
  without the call waiting: a commit made before the snapshot would count as old history. The end
  is answered at once, and the daemon finds the commits after, with two more; a `lookup` waits for
  that, and so does the next call's start for the end's snapshot, as that call could otherwise
  switch branches before it. Measured 2026-10-29 on the Windows laptop, idle, where one `git` process took 24 ms:
  `PreToolUse` for a bracketed call took a median of 79 ms and `PostToolUse` 19 ms, against 15 ms
  and 19 ms for a skipped tool. As first built, the start was four `git` processes and the end
  waited for five: 241 ms and 335 ms on the same machine. With several test runs going, where one
  `git` process took 300 to 400 ms, that start took a median of 1.7 s (0.8 to 2.7 s over five
  runs); the start as it is now wasn't measured that busy, and should take about a quarter of it
  (inferred).
- **It never stops a call.** `PreToolUse` prints nothing and exits 0 whatever goes wrong, never 2.
  It waits at most 5 s for the daemon, which limits its own snapshot to 4 s; one that misses
  credits nothing for that call.
- **Where it is kept.** Each session's credited commits (SHA, branch, subject and when the call
  ended; the latest 100) are in `.git/murmur/state.json` and nowhere else, for as long as its inbox
  is kept after it stops (24 hours) and across daemon restarts. Presence carries only the names of
  the branches it committed to, most recent first, at most 5. A commit made on a detached HEAD is
  kept and `lookup` lists it as that, but it adds no branch to the list.
- **Last active.** The time of the session's last request to the daemon: any hook event (notices,
  or either end of a call) or any MCP call, `who` included. It is held in memory and goes out with
  the next presence write, a change or the 2-minute heartbeat, and never causes one, so another
  device sees it up to two minutes late. A reader works out the time on its own clock from how
  long before the presence was written the session was active, so clocks that disagree don't
  matter. An agent without the hook moves it by MCP calls only. Only Claude Code has commits
  credited: the others' hooks bracket no tool call. An older daemon reads the new presence and ignores both fields; the sessions of a
  daemon that doesn't write them have no last-active time, and the page leaves it blank.
- **Messages in `lookup`** are those the page keeps: the last 500, with the ones from before the
  daemon started read back from the bus (#138).

## Device nicknames

A device's ID (`tyler-laptop-3f2a`) says which machine, not whose. Since 2026-11-11 (#190) a device
can be called something else, such as its owner's name. The issue left five questions open; these
are the answers built, and what each cost.

- **A label, not a new ID, and not an address.** The ID names the device's refs and is in every
  address on the bus, so changing it would orphan the device's log. `who`, `lookup` and the page
  show the nickname beside the ID, and nothing takes it in the ID's place. A first version let
  `send` and `lookup` take `tyler/bd0102`; Sol's review of it sent a message to the wrong device,
  by a nickname that was the ID of a device whose presence had gone. Agents address each other by
  session name anyway, so the address was cut (Tyler, 2026-11-11) rather than guarded.
- **Shared, and set by the device's own user**, with `mur nickname <name>`, or on the page's
  Settings, under "Your account" (#533). It is kept in
  `state.json` and travels in presence, inside the box, so every teammate sees the same name
  without labelling each other's devices. Labels of one's own for other people's devices were left
  out: nobody has asked for two names for one device.
- **The default is the person's name.** A device with no nickname is called by the name its person
  chose on the page ("A person on the page"). Readers work it out from the person in presence, so
  nothing new is written for it. The git `user.name` wasn't used: it is the same on all of one
  person's clones, and `who` shows it anyway.
- **Not unique.** One person may want both their machines called `tyler`, and as a label two
  devices with one nickname cost nothing: the ID is beside it. A nickname is refused only when it
  is a device's ID or the name of another device's person, which would read as someone else. Any
  device can still say any nickname in its presence, so a nickname is no proof of whose a device is.
- **Its form is a person's name's** (`bus.ValidNick`), so it never reads as a session's name, and
  what a presence says is held to that when read.
- Not built: setting it from the page, and the nickname in the header of a message. A message's
  header names its session, and "on <owner>'s machine" the first time.
