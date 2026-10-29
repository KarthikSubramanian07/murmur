# Vision: agents that find each other across a hackathon team

Status: V1 works, in Go (2026-10-26); the next version, with names, a web page and wake-ups, is
being built (2026-10-29). "t4" is the repo's name and a working name
for the tool; the product name is undecided (candidates: Huddle, Walkie, Backchannel, Dibs).

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

1. **Presence.** Who is working, on what device, in which agent, on which branch, doing what.
2. **Direct messages and broadcasts.** For what presence can't settle.

V1 also had claims: before starting something sizeable, an agent took it on a shared ref, and was
shown any claim it hadn't seen. Tyler removed them on 2026-10-28 (#24): "Remove this feature. I
don't want it. This is not part of scope." Daemons still running V1 keep writing
`refs/t4/claims`, which later daemons don't read.

## How it works

### Git is the transport, GitHub the hub

Every teammate already has credentials and push access to the repo, so the repo carries the
messages. No server, no accounts, no extra auth: whoever can push is in.

- Each device appends events to **its own ref**, `refs/t4/mesh/<deviceId>`: one empty commit per
  event, the payload in the commit message (a readable subject, a JSON body). One writer per ref
  means pushes never race. A message to another session sits in the sender's log, addressed to it.
- Per-device logs can't settle a race: two devices could each take the same session name (#20)
  within one 10 s sync, each seeing no rival. What must be settled before anyone acts on it needs
  one shared ref, which GitHub orders. A push to a ref is atomic, and with a lease
  (`--force-with-lease`) it is refused unless the ref still holds what the pusher last fetched,
  which makes the ref a compare-and-swap. A refused pusher fetches and decides again with what it
  hadn't seen. V1's claims worked this way; in its test through GitHub (2026-10-26), two machines
  pushed at the same instant three times, and each time exactly one push landed. Session names
  work this way too, on `refs/t4/names` (see Session names).
- Presence is separate: `refs/t4/presence/<deviceId>`, force-pushed with the device's current
  state when it changes and on a heartbeat every 2 minutes. Heartbeats would otherwise flood the
  event log. A presence unchanged for 6 minutes is stale: its device's sessions may not get what
  is sent to them, and `who` and `send` say so.
- A reader measures staleness from when it saw a presence change, on its own monotonic clock, and
  only up to its own last successful sync, so a reader that was itself asleep or offline doesn't
  take everyone else for gone. Only a presence it has never seen change is dated by the writer's
  clock, and never more than 2 minutes back, so a slow clock can't make a device look gone before
  its next heartbeat.
- Custom refs, not branches, so CI and preview deploys don't fire on every message, and
  `git clone` / `git pull` never fetch them. git-bug uses `refs/bugs/*` on GitHub the same way.
  Tested on this repo on 2026-10-26: a push to `refs/t4/*` produced no `push` webhook delivery
  and no Actions run, even when the pushed commit contained a workflow that runs on every push;
  the same commit pushed to a branch produced both. Vercel and Netlify deploy from GitHub's push
  webhooks, so they have nothing to react to (inferred, not tested with either).
- Writes use git plumbing (`commit-tree`, `push <sha>:<ref>`) and never touch the working tree or
  index, where humans and agents are working at the same moment.
- Bus commits carry git's empty tree, so they hold no files and fetching them brings nothing else
  along. The daemon pushes them with `--no-verify` and doesn't sign them: a `pre-push` hook that
  runs the test suite, or a signing prompt, on every message would make the bus unusable. These
  commits only ever go to `refs/t4/*`, never to a branch.
- beads (task tracking synced through `refs/dolt/data` on GitHub) shows custom refs hold up at
  scale, and two of its bugs are warnings: `git push --mirror` from an ordinary clone deletes
  custom refs on the remote (beads #5266), and checking an internal branch out in the user's repo
  wiped tracked files (beads #6484).
- `mesh/<deviceId>`, `presence/<deviceId>` and `names` are the only names under `refs/t4/` t4
  reads.
  Device IDs are lowercase letters, digits and dashes, never a name Windows can't hold as a file.
  Anything else there is left on the remote unfetched. Anyone who can push can write any of these
  refs, so a reader holds what it reads to what t4 writes and builds over junk rather than on it.
  The remote refuses a ref whose name clashes with another's, so a daemon deletes the refs that
  would stop t4's own being pushed: those under its own names (`presence/<deviceId>/...`,
  `mesh/<deviceId>/...`, `names/...`), and `refs/t4/mesh` or `refs/t4/presence` as refs
  themselves. t4 writes nothing at any of them (found by the Opus reviews of V1, #8, 2026-10-28).
- Reading: poll `git ls-remote origin 'refs/t4/*'` about every 10 s, which is cheap; fetch only
  when a SHA has changed. Delivery takes roughly 10–15 s, plenty for agents: V1 took about 12 s
  from a Windows laptop to a macOS machine through GitHub (2026-10-26), 2 s of it the send.
- A device is one clone's daemon. Worktrees of a clone share its `.git`, so they share its daemon;
  a second clone on the same machine is a second device. The device ID is the hostname plus four
  random hex digits (`tyler-laptop-3f2a`), kept with the daemon's other state in `.git/t4/`, which
  no commit or checkout ever touches. (An earlier draft grouped repos by root commit; per-clone
  daemons don't need that.)

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
to start launches the daemon. Under a job that forbids breakaway, as Codex's MCP servers are on
Windows, the daemon is started through WMI so it outlives that session (2026-10-29). A session's
MCP process can restart while its conversation carries
on: T3 Code restarts it on every interrupt, after 30 minutes idle and on every app restart (one
thread's restarted 8 times in about 2 days, 2026-10-28), and Claude Code on `/mcp` reconnect and
on resume. So a dropped connection starts a 60-second grace (2026-10-28): the session stays in
presence, and a hello with the same session ID in that time takes it over with its inbox and
unread notices, writing nothing to the bus. Only when the grace runs out does the daemon announce
`session.stopped`, even after a crash. A stopped session's inbox is kept 24 hours, across daemon
restarts, and given back if the session returns. It holds only what its daemon received: a daemon
exits 10 minutes after its last session, and one back after longer than that skips what it
missed, so messages sent while it was gone aren't kept. A message is addressed on the bus to
`device/session`, which a session's name stands for (see Session names); presence carries enough
description (name, agent, branch, what it is doing) for an agent to choose a recipient. Nothing sent before a session first started is replayed to it. A
daemon back after less than 10 minutes down delivers what it missed to the sessions that
reconnect within 30 seconds of its start, and to sessions stopped within the 24 hours.

Sessions reach the daemon over loopback TCP, on a random port written with a random token to
`.git/t4/daemon.json`. A daemon with no sessions for 10 minutes exits. Any daemon that stops
deletes its presence, so other devices drop it at their next sync rather than when it goes stale.

### When a session stays the same session

A session's name (#20) lasts as long as its client treats it as the same conversation, so it
follows the client's own ID: a resume keeps the name, and `/clear` or a fork gets a new one
(Tyler, 2026-10-28). Each client keeps and changes that ID at different moments. Researched
2026-10-28 on Claude Code 2.1.310 and 2.1.312, T3 Code 0.1.2 and Codex CLI 0.164.0; the
evidence is in #19.

| Client | The ID | Kept across | New on | Where t4 can read it |
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
- **Under T3 the session is the thread, not the provider's session under it.** The Claude
  session ID under one thread changes when the user switches Claude accounts, and has gone
  A → B → A. T3 gives MCP servers and hooks no way to learn the thread ID. The two ways that
  work today are unofficial: the agent reads it with T3's `t3_thread_configuration` and passes
  it to t4, or t4 looks up the Claude session ID in T3's database. Tyler decided on 2026-10-28
  not to change T3's source, so which of these t4 uses is open.
- **Codex's MCP servers get a bare environment,** about 20 Windows core variables and no
  `CODEX_*`, so the server learns its thread at its first tool call. Hooks under Codex inherit
  the `CLAUDE_CODE_SESSION_ID` of any Claude Code session that started Codex, so that variable
  doesn't prove a hook runs under Claude Code.
- **Two live processes can hold one ID.** Claude Code lets a session be resumed twice at once.

### Session names

Tyler's decisions, 2026-10-28 (#20). Every session has a name of two words, an adjective and a
noun (`quiet-otter`), unique in the repo and never reused, not even after its session ends. The
name is the address: `send` takes it, `who` shows it, and notices and `inbox` name the sender.
`device/session` still works. It is what a message carries on the bus, so the event format didn't
change and a daemon that knows no names delivers by it all the same.

- **Taking a name.** A daemon picks a random name nobody has, commits it on `refs/t4/names` (one
  commit per assignment: the name, the device, t4's session ID), and pushes with the lease on the
  registry tip it last read. The name is the session's only if that push lands. If another device
  got in first the push is refused; the daemon fetches, reads what it missed, and picks again. It
  stops after 5 refusals in a row and backs off, up to 5 minutes.
- **A name follows the client's session ID.** The key is the full `ClaudeSession` the MCP server
  gives when it has one (Claude Code's `CLAUDE_CODE_SESSION_ID`), else the ID t4 gave the session.
  The same key keeps its name across the MCP server restarting, the daemon restarting and a
  resume; different keys never share one. A daemon keeps the key-to-name map in its own state
  file, so the client's ID is never published. A resume of the same client session on another
  device is a new session there, with a new name. Two live sessions with one key, such as two MCP
  servers in one conversation, would share a name, so the later is a session of its own.
- **Never reused, whatever happens to the registry.** A daemon keeps every name it has ever seen,
  in the registry's whole history or in any device's presence, and the set never shrinks, so a
  registry that is deleted, overwritten or force-pushed frees nothing. When the tip isn't a
  readable commit, or its history no longer holds what was read before, the next commit is a new
  root holding a snapshot of every name the daemon has seen, leased on the tip it found. A daemon
  writes that on noticing the damage, not when it next needs a name, so that one joining later
  learns what was taken.
- **Without the remote.** A session that can't get its name works as `device/session`, with its
  name shown as pending, and the daemon keeps trying in the background.
- **Sending to a name.** A name is looked up in the sessions on this device, then in other devices'
  presence, then in the registry, so a message to a session that has stopped is kept for it like
  any other. A name nobody has is an error that says so. A device ID shaped like a name is read as
  a device if no session has that name.
- **V1 daemons.** They don't read the registry, and their presence has no names. Their sessions
  show without one and are reached at `device/session`, and they reach sessions named here the
  same way.

### Delivery differs by client

Sending is an MCP tool call everywhere. Receiving is not, because MCP only answers when asked.
Whatever the route, what reaches the agent is a **notice**: always the sender and the subject,
one line per message, never the body.

```
[t4] quiet-otter (bob-laptop/session-3, claude-code): "Are you changing the auth middleware?"
```

Every message has a subject for this reason. The body is one `inbox` call away, so it arrives as
tool output, as data, not as something that looks like the user typing. Reading the body through
a tool is crosstalk's idea.

| Client | Notice delivered by | Can wake an idle agent |
|---|---|---|
| T3 Code | T3 queues the notice into the thread, as it does for local messages. The preferred route: when a session runs inside T3, use T3 | Yes |
| Claude Code outside T3 | Its inbox socket (`CLAUDE_CODE_MESSAGING_SOCKET`), if it exists; otherwise a hook that checks after tool calls and prompts | With the socket, reportedly; with hooks, no |
| Codex, others | AGENTS.md asks the agent to call `inbox` at task boundaries | No |

The socket exists: Claude Code 2.1.310 sets `CLAUDE_CODE_MESSAGING_SOCKET` (a named pipe on
Windows) and a token for it in every child process, MCP servers and hooks included (seen
2026-10-26). Its protocol, and whether it wakes an idle session, are unverified. Codex has
Claude-style hooks too, `PostToolUse` among them, each run only once the user trusts it in
`/hooks` (0.164.0, 2026-10-28); t4 doesn't install one yet. V1 uses the hook: Claude Code adds a `PostToolUse` hook's `additionalContext` to
the conversation, which is how a notice reaches an agent mid-task (seen with 2.1.310, 2026-10-26).

### The envelope

Every incoming message reaches the agent wrapped in an envelope **the tool adds**, never the
sender: `[t4 message from quiet-otter (bob-laptop/session-3), claude-code]`. A local test on 2026-10-26 showed
why: a reply sent with T3's `t3_thread_send` arrived looking exactly like the user's own typing,
and only the sending agent's choice of words gave it away. Locally that is harmless, since every
sender is the user. Across devices the agent must know a teammate's agent is speaking, not its
user. That is what lets it treat messages as information rather than instructions.

The envelope goes on the `inbox` output, alongside the notice-only delivery above. crosstalk
reports that Claude Code wraps every message pushed through its inbox socket in its own framing
that presents the sender as the user's teammate, and that this can't be turned off (unverified
here). That is one more reason to push only the notice.

## Risks and how the design meets them

- **Agents replying forever.** T3's local messaging stays calm because a reply is plain text in
  the receiver's own thread; nothing goes back unless the agent deliberately sends again. t4 keeps
  that: no automatic replies, incoming messages queued rather than steering a running turn, and
  AGENTS.md wording that asks for coordination, not chatting. No limits or caps on top of that.
- **Prompt injection.** Anyone with push access can now put text in front of your agent. That
  matches the trust model (a hackathon team), but messages never get to bypass the receiving
  user's permission mode; the envelope keeps their origin visible.
- **Leaked secrets.** Everything on the bus stays in repo history, readable by anyone with read
  access, and agents do paste `.env` values. Everything a session sends (its messages and status
  line) is scanned first, and a match blocks the send rather than being redacted, so the
  agent knows what happened. V1 has its own short list of patterns (cloud and API keys, tokens,
  private keys); openhuddle's (`src/shared/scrub.js`) could extend it, license unchecked. The
  need is greater here than for relay-based tools, whose messages expire: these stay in history.

## Why not peer-to-peer

Hackathon Wi-Fi often isolates clients from each other, so LAN discovery fails at exactly the
venues this is for. Across networks, peer-to-peer needs NAT traversal or a relay, which is a
server. GitHub is already where everyone has credentials. The message format is kept independent
of the transport, so a websocket relay can be added later if 10–15 s proves too slow.

## Why general from the start, not T3-only

T3-only would need changes inside T3 itself; from outside, an MCP server is the way in for every
client, T3 included. The daemon and MCP layer cost little more than a T3-only build, and they are
what makes Claude Code and Codex sessions first-class.

## Prior art

A survey on 2026-10-26 found a dozen tools in this space, nearly all from the last few months.
None uses git as the transport: each needs a hub, a relay or a peer-to-peer network that someone
runs or that hackathon Wi-Fi can block. That, and setup amounting to "have push access", is
t4's difference.

- **openhuddle**: a hub server plus Claude Code hooks; claims (automatic, on edit), presence,
  messages. The closest feature match. Days old, one author, many concepts.
- **crosstalk** (epode-studio): encrypted rooms through a Cloudflare relay, eight clients. The best
  message delivery and safety design, the source of notice-only delivery above. Appears macOS-only.
- **walkie**: peer-to-peer chat between agents (Hyperswarm), the most used (≈253 stars). Messages
  only; no relay, so it can fail behind client isolation.
- One machine only, so not comparable: polymatx/dibs, abevz/dibs, bcmyguest/huddle, parley
  (auspex0).

## Built in Go

One static binary per platform, `t4`, holds the daemon, MCP server, hook and CLI. A teammate
installs one file and no runtime. Go cross-compiles for macOS, Linux and Windows from any one
machine with no C toolchain, where Rust would need Apple's SDK and linker to build for macOS
from elsewhere. Tyler chose between the two on 2026-10-26. The MCP server uses the official Go
SDK (`modelcontextprotocol/go-sdk`, v1.8.0). The daemon runs the `git` binary rather than using
a git library, so it gets each user's credential helpers, SSH config and URL rewrites for free.

## Build order

1. Daemon, CLI and git transport, tested with two clones on one machine, then between Tyler's
   Windows laptop and his homeserver (macOS). The homeserver pushes with a deploy key for this
   repo (added 2026-10-26). The test of custom refs against Actions and webhooks is done (above).
2. MCP server and the Claude Code hook. The first useful version.
3. T3: connect as an MCP server. `t3 thread send`, run on the same machine, queues a message
   into any thread and starts its turn, so waking an idle thread (#25) needs no change to T3.
4. Codex specifics.

## Open questions

- The name.
- How does t4 learn a T3 thread's ID: from the agent, or from T3's database? (See above.)
- What is the protocol of Claude Code's messaging socket, and can it wake an idle session?
- Does GitHub rate-limit a `git ls-remote` every 10 s from each of a team's devices? Nothing
  documents a limit for git traffic; none was hit in testing so far.
