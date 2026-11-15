# Delivering a message as a turn

## Delivering a message as a turn

Sending is an MCP tool call everywhere. Receiving is not, because MCP only answers when asked.
Until 2026-10-29 a message reached an agent only as a one-line notice (sender and subject, never
the body) that a hook added after one of the agent's own tool calls, and the agent read the body
with `inbox`. That cannot wake an idle agent, and it leaves a teammate's question unanswered until
the agent happens to call a tool. Tyler's decision (2026-10-29, #57), which replaces notice-only
delivery: **a message from another agent reaches the receiving session as a turn of its own**, the
way a message between two T3 threads does. Murmur's core is two agents talking with nobody typing.

**What "delivered" and "read" mean.** Tyler's rule (2026-11-09, #325), which replaces the one of
2026-11-07 (#187): a message is *delivered* once its recipient knows it is waiting, and *read* once
the recipient has the message itself. The same two words hold for an agent and for a person, so a
sender reads them the same way whoever the recipient is. Before delivered a message is *queued*,
wherever it waits: in the recipient daemon's inbox, in a T3 thread's queue behind a running turn,
or for the hook's next run. These are what a sender acts on, so neither may be said early: a looser
word would let a sender believe an agent knows something it hasn't seen, and may not see for hours
if it is idle and nothing wakes it (#25). #187 used *delivered* for what is now *read*; the change
was made so that a person's inbox and an agent's use one pair of words. The words, in the order a
message meets them:

- *Queued:* the recipient's daemon has put it in the session's inbox. The daemon knows.
- *Handed over:* the daemon has passed it to the session's client, by `t3 thread send`, the
  messaging socket, or the client's own command line. Still queued, in the client now, and out of
  the daemon's inbox.
- *Delivered:* the recipient knows it is waiting. For an agent, the hook's notice (sender and
  subject) is in its context. For a person, it is in their inbox on the page, counted as unread;
  the page can't tell whether they looked.
- *Read:* the recipient has the message. For an agent, the body is in its context. For a person,
  they opened the conversation, which marks it read (a hidden page, or one in a background
  window, marks nothing).

A message that arrives whole (a turn, the hook's whole message, the `Stop` hook's continuation)
is read then. If no notice told of it first, it skipped delivered: so does every message to an
agent the hook doesn't run in, which gets no notice. One the hook's notice told of during a turn
was delivered then, and is read later, by whichever of these comes first; the turn that gives it
whole then says a notice told of it, so the agent doesn't take it for a second message (#355). Where each moment is for an agent, by route:

| Route | Delivered or read when | Can Murmur see it |
|---|---|---|
| `t3 thread send` | read when the turn carrying it starts: at once in an idle thread, after the running turn in a busy one (a busy one only where the hook doesn't run, #202) | No. The daemon sees `t3` accept the message, not the turn start; T3's database would show it (`orchestration_v2_projection_messages`), but those tables are T3's own and may change |
| The messaging socket (Claude Code outside T3; Pi's extension's, outside T3) | read when the client takes it as a turn | No: the socket answers nothing |
| The client's command line (`codex queue`, `opencode api`; Codex and OpenCode 2 outside T3, #135) | read when the turn carrying it starts: at once in an idle session, after the running turn in a busy one | No. The daemon sees the command accept the message, not the turn start |
| The hook's notice (Claude Code, and Pi through its extension) | delivered when the hook's `additionalContext` is added, after the agent's next tool call or at its next prompt, with the sender and subject. Where the page's busy setting says whole message (#204), it is read then instead. So is a message with no body, whatever the setting: its notice would show all of it, and until 2026-11-09 it stayed unread and came again as a turn (#355). A message told of is read later by `inbox`, by the `Stop` hook keeping the turn going with it, or as a turn handed over when the turn ends | Partly: the daemon sees the hook fetch the notices (`Noticed`), not the hook's output reach the agent |
| `inbox` | read when the tool's result returns to the agent | Partly: the daemon sees the messages leave in its answer, not the answer reach the agent through MCP |

Receipts report these states back to the sender (#26; "Receipts", below): queued, handed over,
delivered and read as different states, and a handed-over message as read only once something can
see the turn start, which nothing can yet.

The daemon hands a message over at once, when it arrives for a session it can reach, unless the
hook has the session at work (see "A session at work", below):

| Where the session runs | Handed over by | Starts a turn in an idle session |
|---|---|---|
| T3 Code: Claude Code, Codex, OpenCode or Muse (and Pi and Grok, not tried) | `t3 thread send <thread> -`, the text on standard input. T3 starts the thread's turn if it is idle and queues the message as its next turn if it is busy, interrupting nothing. A busy session the hook runs in (Claude Code, Codex, Grok, and Pi through its extension) is not handed the message: the hook tells it | Yes (seen 2026-10-29: Claude Haiku to Claude Haiku, and Claude Haiku to Codex gpt-6.1-sol, in two real T3 threads, nobody typing) |
| Claude Code outside T3 | the session's messaging socket | Unverified: a message Murmur's code wrote to a session's socket arrived as a turn while the session was working (2026-10-29, Claude Code 2.1.312, Windows); an idle one has not been tried |
| Pi outside T3 | the socket Murmur's extension opens in the session | Yes (2026-11-09, Pi 1.5.0 in RPC mode with a stand-in model: an idle session took a message as a turn, nobody typing) |
| Codex, OpenCode, Muse or Grok outside T3, or any handoff that fails | the hook's notice where the hook runs (Claude Code, Codex, Grok, Pi, OpenCode 2), and `inbox` | No |

What the agent reads:

```
Message from quiet-otter (Claude Code, on smash's machine) via Murmur:

<the subject, then the body>

Reply with Murmur's `send` to quiet-otter if a reply is needed.
```

The first and last lines are Murmur's: the first says that a teammate's agent is speaking, not the
user (the job the old "envelope" did, so that the agent treats a message as information rather
than as the user's own typing), and the last says how to answer and that answering is optional.
The sender's text in between is passed on as written, neither quoted nor wrapped: teammates'
agents are trusted in Murmur's model, and a message that reads as a request is meant to be acted on.
A body past the limit is cut, as in `inbox`.

- **Who it is from, as the receiver knows it.** The first line is written when the handoff starts,
  which is as soon as the message is queued, from what the receiving daemon knows of the sender
  then: the name from presence or the registry, the agent and owner from presence. A sync reads
  the registry and every presence before the logs, so what landed before the message is known
  when it is queued. Those reads have 15 s (`ReadFirstLimit`), and never more than half of what is
  left of the sync, which keeps at least as much, less a cancelled git's up to 5 s to exit, for the
  logs, already fetched: a registry missing from the listing is asked of the remote again, and a
  stall there would otherwise use up the minute. A sync that runs past the limit still reads the
  logs, and then fails; the next sync reads what it missed. A fixed reserve of 15 s, tried first,
  skipped these reads altogether in the 10 s sync a daemon runs before it accepts sessions. Until 2026-11-09 the logs came first, and a sync that fetched a sender's
  name with its message wrote the turn without it (#71). A sender whose presence is pushed after
  its message still arrives without its agent, and without its name if it took one from the pool
  and the assignment hasn't landed either.
- **Out of the inbox when handed over.** A message handed over leaves the receiving session's
  inbox, so neither the hook nor `inbox` shows it again: the client has it now, and gives it to the agent as
  a turn. The page and the daemon's log keep it as history. A message that could not be handed over
  stays in the inbox and is announced by the hook as before. The hook leaves alone a message that
  is waiting its turn to be handed over, behind the one being handed over: announced by a notice it
  would never become a turn.
- **A session at work (#202).** Until 2026-11-08 a message for a busy T3 thread went to
  `t3 thread send` like any other, and T3 queued it behind the running turn. The agent heard
  nothing until the turn ended, Murmur couldn't tell whether T3 ever played the queued turn, and
  T3 holds such turns rather than starting them after a run the user stopped, while the provider
  is usage-limited, and while the thread's mode is above its ceiling (T3 source at f558007b, read,
  not observed); a person can also delete one in T3's window. Tyler's decision (2026-11-08): where
  Murmur's hook runs, a busy session isn't handed the message. The hook tells it after its next
  tool call, sender and subject with the count unread, and the agent reads the body with `inbox`
  when it chooses. What it was told of and hasn't read when the turn ends is given to it then,
  however long the turn took, so nothing waits for good, and so is a message that came during the
  turn with no tool call after it.
  - *At the end of the turn* the `Stop` hook keeps the turn going with those messages: it blocks
    the stop, and Claude Code gives the agent the hook's reason, each message as its turn would
    read (Tyler, 2026-11-08; first built as a new turn, handed to T3 or the socket). That needs
    neither T3 nor a messaging socket, and nothing goes into T3's queue, which may hold a turn for
    good (above). The cost is a longer turn: its user sees it go on after what looked like the
    agent's last word. The messages are read once the daemon answers the hook, so a hook that
    gave up waiting (3 s) just before the answer came loses them, as a whole message can (#204).
    The session is at work again, so a message that comes now waits for the turn's next end.
    Outside T3 that lasts 2 minutes unless another hook of the session shows the turn went on: an
    answer that was lost leaves an agent that stopped, and nothing else there would end its turn.
    The page's wake setting decides as it does for a turn: under never, they stay in the inbox. A
    turn that ends without `Stop`, one the user interrupted in T3, has them handed over as a turn
    instead, and so does a hook that says it can't keep a turn going (`Continues` unset). So does
    a ninth `Stop` in a row with no tool call between: Claude Code ends the turn after eight
    continuations whatever the hook says (its hooks reference, 2026-11-08), so the daemon counts
    them. The user's own Stop hooks count toward that cap too, and the daemon doesn't see them.
    The first `Stop` that can keep a turn going also gives what the session was told of before
    it, when there may have been no way in. Not seen in the world: a T3 thread or terminal
    session kept going by the hook; the `decision: block` answer is Claude Code's documented one.
  - *Busy and idle come from the hook*: a prompt or a tool call means busy, and the `Stop` hook,
    which `mur join` installs since this change, means the turn ended. "No tool call lately" would
    not do, as one long command looks idle. A turn the user interrupts fires no `Stop`; in T3 the
    busy state also ends when a read of T3's database that began after the hook last ran finds no
    run of the thread queued or running. Comparing the run's recorded end with the hook's last call
    was tried first and dropped in review (Sol, 2026-11-08): a hook that comes after the run's end,
    such as one from a tool call the interrupt cut short, would have left the session busy for
    good. A turn the database ends this way is ended as `Stop` ends one: what came during it and
    wasn't told is held too. A session whose MCP connection dropped keeps its state through its
    grace, and one that reconnects after the daemon restarted (an update, say) counts as busy while
    T3 shows its thread's run going, and for its first minute until its thread has been read at
    all, until its own hook runs again (a subagent's doesn't count): without that, what was held
    for the end of its turn would go to T3's queue mid-turn. Outside T3 nothing ends it until the next prompt and the `Stop` after it, so a message to
    an interrupted session waits for the user to come back. An update doesn't run `mur join`
    again, so a clone joined before this change has no `Stop` hook until its user does; outside T3
    a session is held back only once the daemon has seen a `Stop` from it, and until then a
    message is handed over as a turn, as before.
  - *Subject or whole message (#204).* What the hook tells a busy session is the page's busy
    setting, for the device or for one session, beside the wake setting (Tyler, 2026-11-08).
    "Subject", the default, is the line above. "Whole message" is the message as its turn would
    read, a long body cut the same way, and the message is then read: it leaves the inbox and
    the count, and the page and the daemon's log say it went through the hook. Subject leaves the
    agent's focus to the agent, at the cost that it may pass over a line it needed; whole puts
    every message in front of it mid-task, at full length, whether or not it matters to the task.
    A message given whole counts as read once the daemon answers the hook, so a hook that gave
    up waiting (3 s) just before the answer came loses it. One that was handed to T3 or the socket
    before, and may be there already, gets the subject only, as does a hook from before the
    setting, which prints only subjects: its request doesn't say it prints whole messages, and the
    message stays in the inbox (#229). Where the hook doesn't run, the setting does nothing.
  - *The count* is shown with each new message, and again only when it changes, not after every
    tool call. A fall to none isn't told: the agent read them itself.
  - *Held however long.* A message that came during a turn goes in at its end whatever its age:
    the 15 minutes a message may wake a session (below) measure an absence, and a turn can be
    longer. One from before the turn, told at its start, is held only if it is still within them;
    older, it stays a notice, as it would have without the turn.
  - *Why not hand it to T3 as well*: T3 can't take back a queued message, so the agent could read
    it with `inbox` mid-turn and then get it again as a turn.
  - *Where the hook doesn't run* (OpenCode 1 and Muse in T3, as of 2026-11-12), nothing tells the
    daemon the session is at work, and a message goes to T3 as before: without the handoff the
    session would hear nothing at all. Codex and Grok have the hook since #225, Pi since #205, and
    OpenCode 2 since #431.
  - *Subagents.* A subagent's tool call carries the parent's `session_id`, with `agent_id` set
    (Claude Code 2.1.312, 2026-10-29). The hook tells it nothing, as its notice would land in the
    subagent's context, not the session's (inferred, not seen; #188), and it doesn't mark the
    session at work: a background subagent goes on after its parent's turn has ended.
  - *Not covered:* a single tool call that runs a long time gets no notice until it ends. Whether
    a busy Claude thread in T3 shows the hook's notice mid-turn is read from T3's source (its
    Claude adapter leaves the Agent SDK's `settingSources` unset, so project hooks load), not
    watched.
  - *Considered and not chosen:* a background watcher the agent starts (it has to restart it after
    every message, and Codex isn't told when a background process ends); Claude Code Channels
    (events that come while Claude is busy are held for the next turn, and it is a research preview
    the Agent SDK ignores); steering the running turn with Codex's `turn/steer` or T3's
    `t3_thread_send` in `steer` mode, which is the interruption the 2026-10-29 rule avoids.
- **When a handoff fails.** A failed handoff is left to the hook's notice, and the log
  says why, once per session. It is not tried again, except when it plainly never started (no `t3`
  command to run, no socket to connect to), which is tried once more after a pause (2 s). Any other
  failure may have come after T3 took the message: `t3 thread send` has no idempotency key (the
  MCP tool `t3_thread_send` has `clientRequestId`, the command line doesn't), and the messaging
  socket answers nothing, so a second call could hand the message over twice, as two turns. The cost
  is that a failure that was only a failure (T3 starting up, say) gets a notice and no second try.
  The mark that a message was handed over ("attempted", in `state.json`) is saved before the
  handoff, not after, so it holds when the daemon stops in the middle of the call or right after a
  failed one; a daemon that starts again within the 15 minutes finds it and leaves the message to the
  notice, where it would otherwise have sent the turn a second time (#82). The mark comes off only
  when the call provably went nowhere, as above (that includes a `t3` call cancelled before it could
  start). A mark that can't be saved stops the handoff: the message is a notice, as after any failed
  handoff. The save is a write to the file, which a power cut can still lose. A crash between the
  save and the handoff leaves the mark on a message that was never sent, which is the safe way
  round: it is a notice, not a repeat.
  The other safe way is to look in T3's database for the message before trying again
  (`orchestration_v2_projection_messages`, by thread and text, or `t3 thread show --json`); it
  would keep the second try, and it relies on tables that are T3's own and may change in any
  update, so it wasn't done. Nothing is handed over for a user who has left either (see Joining a
  repo). A message that waited more
  than 15 minutes (T3 closed, the session away) is not sprung on the agent as a turn: it stays a
  notice. The 15 minutes count from when the daemon puts the message in the inbox, so one read
  after a long outage (2026-10-30, #30) is young and wakes its thread.
  The page's wake setting (#22, #25), for the device or for one session, decides the rest.
  "Never" keeps the notice. "Always" hands over at once. "Within N minutes", the default with N = 3,
  hands over only to a session that is busy or was last at work no more than N minutes ago; one idle
  longer gets the notice, and the message is handed over as a turn after all if the session is at work
  again while it is still young enough. The reason for the limit: a session idle that briefly
  still has its prompt cache, and a turn started after the cache has gone reads the whole
  conversation again at full price. Until 2026-11-07 "within" handed over as "always" did, which
  Tyler found out from the page; the minutes were saved and nothing read them. For a session in
  T3, "at work" comes from T3's database: busy while a run of its thread is queued or running,
  else idle since its latest run ended (`orchestration_v2_projection_runs`). Any other session, or
  one whose thread hasn't been read yet, goes by its start, its last request or tool call, and,
  stopped, when it stopped; a message just handed over as a turn counts too. The database is read
  every 5 s, so a decision can be that old. A daemon that starts looks at the messages its
  inboxes kept, and hands over those that are still young enough and may be.
- **No loop guard.** Nothing in Murmur limits how often two agents answer each other (Tyler's
  decision, 2026-10-29). A message to a busy thread never steers the turn running mid-task: it is
  queued as the next turn, or, where the hook runs, announced, and given at the turn's end, which
  the turn then goes on with (#202). The AGENTS.md wording asks for coordination, not chatting; beyond that
  only the agents stop a loop.
