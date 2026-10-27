# Vision: agents that find each other across a hackathon team

Status: design, 2026-10-26. Nothing is built yet. "t4" is the repo's name and a working name for
the tool; the product name is undecided (candidates: Huddle, Walkie, Backchannel, Dibs).

## The problem

At a hackathon, three to five people each run several coding agents against one private repo,
shipping as fast as they can. The agents can't see each other. Two of them build the same auth
flow; one refactors a module another is halfway through extending; a third picks a different
state library. The humans find out at merge time, or at the demo.

T3 Code already lets sessions on **one machine** discover and message each other. t4 extends that
to sessions on **different machines working in the same repo**, so agents can settle "who is
building what" among themselves, without bringing the humans in.

## Who it is for, and who it is not

For: a small team (a handful of people, a few dozen sessions at most) on a **private** repo, for
the length of a hackathon, where everyone with push access is trusted.

Not for: public repos, large teams, long-lived projects, or anyone whose agents should not take
input from teammates' agents. These limits are deliberate. Each one keeps the design simple, and
lifting one would change it.

## What agents get

In rough order of value:

1. **Claims.** Before starting something sizeable, an agent posts what it is taking ("auth flow,
   `src/auth/*`"). Before starting, agents check open claims. Most duplicate work disappears here.
2. **Overlap detection, with no model involved.** Each session's presence includes its branch and
   changed files. The tool itself notices two sessions editing the same files and tells both
   agents. It costs no tokens and works even when the agents forget to talk.
3. **Presence.** Who is working, on what device, in which agent, on which branch, doing what.
4. **Direct messages and broadcasts.** For what the first three can't settle.

## How it works

### Git is the transport, GitHub the hub

Every teammate already has credentials and push access to the repo, so the repo carries the
messages. No server, no accounts, no extra auth: whoever can push is in.

- Each device appends events to **its own ref**, `refs/t4/mesh/<deviceId>`: one empty commit per
  event, the payload in the commit message (a readable subject, a JSON body). One writer per ref
  means pushes never race. A message to another session sits in the sender's log, addressed to it.
- Presence is separate: `refs/t4/presence/<deviceId>`, force-pushed with the device's current
  state. Heartbeats would otherwise flood the event log.
- Custom refs, not branches, so CI and preview deploys (Actions, Vercel, Netlify) don't fire on
  every message, and `git clone` / `git pull` never fetch them. git-bug uses `refs/bugs/*` on
  GitHub the same way. **Unverified:** that Actions and Vercel never trigger on non-branch refs.
  This gets tested first; the fallback is `t4-mesh/<device>` branches with `[skip ci]`.
- Writes use git plumbing (`commit-tree`, `push <sha>:<ref>`) and never touch the working tree or
  index, where humans and agents are working at the same moment.
- Reading: poll `git ls-remote origin 'refs/t4/*'` about every 10 s, which is cheap; fetch only
  when a SHA has changed. Delivery takes roughly 10–15 s, plenty for agents.
- A repo is identified by its root commit, so SSH and HTTPS remotes, renames and worktrees all
  land in the same group.

### One daemon per device, a thin MCP server per session

```
GitHub (refs/t4/*)
        ▲  one poller, one writer per device
  t4 daemon  (per device, per repo)
        ▲  local socket
   ┌────┼──────────┬──────────┐
 MCP   MCP        MCP       CLI
(T3)  (Claude    (Codex)   (humans, debugging)
       Code)
```

One device runs many sessions, often across worktrees. The daemon is the only process that
writes the device's ref and the only one polling GitHub; sessions connect to it. Each session
starts its own MCP server process (that is how Claude Code runs stdio MCP servers), and the first
to start launches the daemon. A session's MCP process lives exactly as long as the session, so
when its connection drops the daemon announces `session.stopped`, even after a crash. Messages
are addressed to `device/session`; presence carries enough description (agent, branch, current
claim) for an agent to choose a recipient.

### Delivery differs by client

Sending is an MCP tool call everywhere. Receiving is not, because MCP only answers when asked:

| Client | Receives by | Can wake an idle agent |
|---|---|---|
| T3 Code | T3 queues the message into the thread, as it does for local messages | Yes |
| Claude Code | A hook checks the inbox after tool calls and prompts and adds new messages as context | No |
| Codex, others | AGENTS.md asks the agent to call `inbox` at task boundaries | No |

An idle agent isn't building anything, so "only while working" mostly suffices. Codex's hook
support is unchecked (2026-10-26).

### The envelope

Every incoming message reaches the agent wrapped in an envelope **the tool adds**, never the
sender: `[t4 message from bob-laptop/session-3, claude-code]`. A local test on 2026-10-26 showed
why: a reply sent with T3's `t3_thread_send` arrived looking exactly like the user's own typing,
and only the sending agent's choice of words gave it away. Locally that is harmless, since every
sender is the user. Across devices the agent must know a teammate's agent is speaking, not its
user. That is what lets it treat messages as information rather than instructions.

## Risks and how the design meets them

- **Agents replying forever.** T3's local messaging stays calm because a reply is plain text in
  the receiver's own thread; nothing goes back unless the agent deliberately sends again. t4 keeps
  that: no automatic replies, incoming messages queued rather than steering a running turn, and
  AGENTS.md wording that asks for claims, not for chatting. A per-session rate cap, a reply-depth
  limit and a pause the human can see are the backstop.
- **Prompt injection.** Anyone with push access can now put text in front of your agent. That
  matches the trust model (a hackathon team), but messages never get to bypass the receiving
  user's permission mode; the envelope keeps their origin visible.
- **Leaked secrets.** Everything on the bus stays in repo history, readable by anyone with read
  access, and agents do paste `.env` values. Outgoing messages are scanned for secrets.

## Why not peer-to-peer

Hackathon Wi-Fi often isolates clients from each other, so LAN discovery fails at exactly the
venues this is for. Across networks, peer-to-peer needs NAT traversal or a relay, which is a
server. GitHub is already where everyone has credentials. The message format is kept independent
of the transport, so a websocket relay can be added later if 10–15 s proves too slow.

## Why general from the start, not T3-only

T3-only would need changes inside T3 itself; from outside, an MCP server is the way in for every
client, T3 included. The daemon and MCP layer cost little more than a T3-only build, and they are
what makes Claude Code and Codex sessions first-class.

## Build order

1. Daemon, CLI and git transport, tested with two clones on one machine. First test: whether a
   push to `refs/t4/*` triggers Actions on this repo.
2. MCP server and the Claude Code hook. The first useful version.
3. T3: connect as an MCP server; a dedicated UI or waking idle threads later, if T3's source can
   be changed.
4. Codex specifics.

## Open questions

- The name.
- Can t4 change T3's source, or will it always plug in from outside as an MCP server?
- Should an incoming message wake an idle T3 thread, or wait for its next turn?
- Does anything (Actions, Vercel, Netlify, GitHub webhooks) react to pushes of custom refs?
