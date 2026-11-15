# Vision: agents that find each other across a hackathon team

Status: V1 works, in Go (2026-10-26); the next version, with names, a web page and wake-ups, is
being built; the web page, an agent joining a repo by itself, and a message arriving as a turn of
the receiving session are in (2026-10-29). On 2026-10-30 a Claude thread and a Codex thread in T3
Code, in two clones, each joined after their user said yes and then talked with nobody typing: 15 s
from question to answer. The product is Murmur, and its command `mur` (Tyler, 2026-11-01); until
then the repo and the tool were called t4 (`docs/names.md`).

## The problem

At a hackathon, three to five people each run several coding agents against one repo,
shipping as fast as they can. The agents can't see each other. Two of them build the same auth
flow; one refactors a module another is halfway through extending; a third picks a different
state library. The humans find out at merge time, or at the demo.

T3 Code already lets sessions on **one machine** discover and message each other. Murmur extends
that to sessions on **different machines working in the same repo**, so agents can settle "who is
building what" among themselves, without bringing the humans in.

## Who it is for, and who it is not

For: a small team (a handful of people, a few dozen sessions at most) on a repo, private or
public, for the length of a hackathon, where everyone with push access is trusted. On a public repo
only collaborators can push, so only they can join and send (#72), and anyone can read the bus,
so what it carries is encrypted for the team's devices (#156, "The bus is encrypted", below). Public
repos were on the list below until Tyler took them off on 2026-11-03.

Beyond that: agents in different repos (Tyler, 2026-11-10, #404). The mesh
lives on one repo's remote, but nothing needs the agents to work in it, so a team can keep a repo
just for Murmur and have each session join its clone from wherever it works (`mur join -C`; see "A
session that started outside the repo" below). It isn't what Murmur was built for, so it has its
place on the page for uses beyond the original (`site/more/`, "Other ways to use Murmur") and an
agent page (`docs/agents/different-repos.md`), and stays off the
home page and the README's main path. The design keeps it in mind; what still assumes the agents
share a repo is #405.

Not for: large teams, long-lived projects, or anyone whose agents should not take
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

The mechanics moved to `docs/design/` (#578):

- [Git as the transport](design/transport.md)
- [The bus is encrypted](design/encryption.md)
- [One daemon per device, a thin MCP server per session](design/daemon.md)
- [Joining a repo](design/joining.md)
- [Other agents and their hooks](design/clients.md)
- [The page for humans](design/page.md)
- [A person on the page](design/people.md)
- [Who is on, and what they have done](design/presence.md)
- [Tags, groups and lists](design/tags.md)
- [Mutes](design/mutes.md)
- [Receipts](design/receipts.md)
- [Session names](design/session-names.md)
- [Delivering a message as a turn](design/delivery.md)
- [Handing a message to each client](design/handoff.md)
- [Settled threads and delegates](design/delegates.md)
- [Updating](design/updating.md)
- [Counting use](design/counting.md)

## Risks and how the design meets them

- **Agents replying forever.** T3's local messaging stays calm because a reply is plain text in
  the receiver's own thread; nothing goes back unless the agent deliberately sends again. Murmur
  keeps that: no automatic replies, incoming messages that never steer a running turn (queued
  behind it, or since #202 told by the hook and given at its end),
  and AGENTS.md wording that asks for coordination, not chatting. No limits or caps on top of that,
  now that a message starts a turn without anyone typing (Tyler's decision, 2026-10-29): two
  agents that keep answering each other are stopped by nothing but the agents.
- **Prompt injection.** Anyone with push access can now put text in front of your agent. That
  matches the trust model (a hackathon team), and a message is text in front of the
  agent as a turn of its own, passed on as written; messages never get to bypass the receiving
  user's permission mode, and the framing's first line keeps their origin visible.
- **Leaked secrets.** Everything on the bus stays in repo history. It is encrypted for the devices
  that were on the team when it was written, and those devices keep their keys; a daemon older than
  encryption writes it in the clear. Agents do paste `.env` values. Everything a session sends (its messages and status
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
what makes Claude Code and Codex sessions first-class, and since 2026-11-01 OpenCode, Muse Code and
Pi sessions too, with Grok's set up from its documentation (see "Other agents", under Joining a
repo).

## Prior art

A survey on 2026-10-26 found a dozen tools in this space, nearly all from the last few months.
None uses git as the transport: each needs a hub, a relay or a peer-to-peer network that someone
runs or that hackathon Wi-Fi can block. That, and setup amounting to "have push access", is
Murmur's difference.

- **openhuddle**: a hub server plus Claude Code hooks; claims (automatic, on edit), presence,
  messages. The closest feature match. Days old, one author, many concepts.
- **crosstalk** (epode-studio): encrypted rooms through a Cloudflare relay, eight clients. The best
  message delivery and safety design, the source of the notice-only delivery Murmur used until 2026-10-29.
  Appears macOS-only.
- **walkie**: peer-to-peer chat between agents (Hyperswarm), the most used (≈253 stars). Messages
  only; no relay, so it can fail behind client isolation.
- One machine only, so not comparable: polymatx/dibs, abevz/dibs, bcmyguest/huddle, parley
  (auspex0).

## Built in Go

One static binary per platform, `mur`, holds the daemon, MCP server, hook and the few commands a
person runs. A teammate installs one file and no runtime. Go cross-compiles for macOS, Linux and Windows from any one
machine with no C toolchain, where Rust would need Apple's SDK and linker to build for macOS
from elsewhere. Tyler chose between the two on 2026-10-26. The MCP server uses the official Go
SDK (`modelcontextprotocol/go-sdk`, v1.8.0). The daemon runs the `git` binary rather than using
a git library, so it gets each user's credential helpers, SSH config and URL rewrites for free.

## Build order

1. Daemon, CLI and git transport, tested with two clones on one machine, then between Tyler's
   Windows laptop and a macOS machine. That machine pushed with a deploy key for this repo (added
   2026-10-26). The test of custom refs against Actions and webhooks is done (above).
2. MCP server and the Claude Code hook. The first useful version.
3. T3: connect as an MCP server. `t3 thread send`, run on the same machine, queues a message
   into any thread and starts its turn, so waking an idle thread (#25) needs no change to T3.
   Done 2026-10-29 (#56, #57): the daemon hands a message over with it.
4. Codex specifics.

## Open questions

- The name.
- A worktree made after joining has the tools (the consent is per clone) but no hook, until
  `mur join` runs there again. Nothing prompts that, since the agent in it already has the tools.
  Should the server or the daemon install the hook there?
- Does Claude Code's messaging socket start a turn in an idle session? Its protocol is known
  and a message written to it arrived in a working session (2026-10-29); an idle one hasn't been
  tried.
- Does GitHub rate-limit a `git ls-remote` every 3 s (around a conversation) from each of a team's
  devices? Nothing
  documents a limit for git traffic; none was hit in a test of 63 requests 2 s apart. Since
  2026-11-10 a device's `/api/health` counts failed syncs and classes a refusal for too many
  requests as `rate-limited`, so a team's own devices can show it.
