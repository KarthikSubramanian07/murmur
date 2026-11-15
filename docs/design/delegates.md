# Settled threads and delegates

- **Settled T3 threads are offline** (Tyler, 2026-11-07, #189). A thread T3 shows as settled
  (`settledOverride` in the thread's payload), archived or deleted leaves presence, so `who`,
  `lookup` and the page show it gone; its device lists it in presence's `offline`, and a daemon
  that reads that refuses a `send` to it, as its own device does. A message that reaches the
  device anyway (from a daemon older than the field) is dropped. Unsettled, which T3 does when
  someone types in the thread, it is back with the next read. T3 settles many threads by itself,
  often the moment a turn ends (their `settledAt` is the run's end; 671 of 854 threads on the
  laptop were settled, 2026-11-07), so this takes most finished T3 threads off the mesh.
- **Delegates go through their parent** (Tyler, 2026-11-07, #188). A thread another thread
  started, a T3 `delegate_task` child or a provider's subagent thread, has its own MCP server and
  registered as a session of its own, with a name and an inbox (reproduced 2026-11-07). T3 marks
  such a thread with `lineage.parentThreadId` (and `relationshipToParent: "subagent"`; 624 such
  threads on the laptop that day, none a user started), so the same read that finds a settled
  thread finds it, and it is off the mesh the same way: out of presence, listed in `offline`,
  messages to it dropped and refused, the refusal naming the parent's session on its own device.
  It can't `send` or set a status; the refusal tells it to ask the thread that started it. Its
  commits are credited to the session of its parent thread, if that is on the mesh here. It may
  have taken a name in the seconds before its thread was read; nobody sees the name. A Claude Code
  subagent inside one session never registered (see Crediting commits, Subagents).
  *Pi (2026-11-11, #457)* has no subagents of its own; packages add them. `pi-subagents` (0.31.0)
  runs each as a child `pi` that loads the user's extensions, Murmur's among them, and so
  registered as a session. It sets `PI_SUBAGENT_CHILD=1` in the child, and Murmur's extension
  does nothing in a process that has it: no server, hook or socket (seen with Pi 0.99.0: such a
  process never reached the daemon). This is not the daemon's delegate path, as the child never
  registers. Not covered: `@gotgenes/pi-subagents` (16.6.0) makes its subagents inside the
  parent's process and binds the parent's extensions to each; nothing in the environment tells
  such a session from its parent, and nobody has run one to see whether it registers.
- **The subagents Codex spawns are delegates too** (Tyler, 2026-11-12, #454), outside T3 Code,
  where T3's database knows nothing of them. It matters more since #447: such a thread could be
  handed a turn nobody asked for.
  - *The mark is Codex's own.* Its state database (`state_N.sqlite`, `threads`) says who started
    a thread: `source` is a JSON object with a `subagent` key for a thread Codex made for another
    (one a thread spawned, a review, a guardian), and a plain word for one a person's client
    started. For a spawned thread it names the thread that spawned it, which is the parent the
    refusal points to and the commits are credited to.
  - *The call says so too* (#487, protocol 20). A subagent's tool calls carry, in `_meta`'s
    `x-codex-turn-metadata`, `thread_source` "subagent" and `parent_thread_id`; a person's say
    "user". Codex starts a subagent's MCP server only for its first call to that server's tools,
    and the call follows within about 15 ms; a subagent that calls none of Murmur's tools has no
    Murmur server at all. A person's thread gets its servers when the thread starts. (Codex 0.164
    and 0.167, `codex exec` and `codex app-server`, a scratch MCP server logging what it got,
    2026-11-12.) So a Codex server outside T3 holds its first hello for up to a second for a call,
    and tells the daemon what the call said before the call goes on: a subagent registers as a
    child, is never listed, its start and stop are never logged, and its first `send` is refused.
    A call that comes while the hello is on its way is held until the daemon has the route. If
    the daemon doesn't take the route, the call goes on as an ordinary session's, and Codex's
    state marks it later, as it still does for servers from before #487. The cost: `codex exec`
    fires its first prompt's hook as the thread starts, before the held hello, so that one hook
    finds no session.
  - *The thread's ID* comes only from a call to a Murmur tool, which is where a Codex MCP server
    learns it. Codex's hook reports an ID earlier, at the first prompt, but it is paired with a
    session by process, and a subagent runs in its parent's process: its hook can be paired
    with the session that spawned it. So a hook's ID marks nobody.
  - *Inside T3* nothing is read from Codex. Tyler's rule (2026-11-12, #452) is that a thread
    shown in the user's list of sessions is on the mesh, and for T3's own threads T3's database
    says which those are (#188).
  - *No handling for subagent tools* (Tyler, 2026-11-12, #481). A tool that runs Codex as another
    agent's subagents, such as Tyler's own Claude Code plugin codex-sub, asks Codex for threads
    as any client does, and Codex records them as it does a terminal's. #473 first recognised
    codex-sub by the name it gives Codex (`threads.originator`) and found its parent among the
    ancestors of its Codex process; that was special handling in a shared tool for one person's
    plugin, and came out before the merge. Such a thread is on the mesh like any Codex session.
  - *What is left.* Its parent is named only if the thread that spawned it has called a Murmur tool
    too, since that is how a session's thread is known; otherwise the refusal names no one and
    the child's commits stay its own. Nothing is saved: after a daemon restart a Codex child is
    marked again at the first read, as its MCP server still gives its thread, and a child a hook
    named is back until its next hook. A child found late keeps the name it took; nobody sees
    it. A Codex subagent of a thread in T3 is left to T3, which doesn't list it; whether it is on
    the mesh there wasn't looked at. Murmur's own server was not run under a live Codex subagent:
    Codex's side was seen with the scratch server, and Murmur's with stand-ins.
- **A hook can say its session is a child** (2026-11-12, #452, protocol 19). Where the client knows a
  session's parent and Murmur has a hook in it (OpenCode's plugin, #455), the hook's request
  carries `child` and, if it knows, `parentNative`, the client's own ID of the session that
  started it. The daemon keeps that by the session's own ID, so it holds for a session whose MCP
  server registers after the hook spoke, and the session is a delegate from then on. Nothing
  takes the mark off: a session that was started by another doesn't stop being so.
