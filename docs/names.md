# Candidate names

t4 is a working name, and the product name is undecided. A rename touches only `internal/brand`.

Top picks (2026-10-29):

- **Gaggle**
- **Foghorn**

Intra-repo agent communication.
Your agents work better as a team.

Tyler's candidates (2026-10-29):

- **IRAC**: intra-repo agent communication
- **CDAC**: cross-device agent communication
- **IRASC**: intra-repo agent swarm communication
- **IRMS**: intra-repo messaging service

## What a name has to survive

The name is `brand.Name`, and everything below is built from it: the command (`<name> join`), the
bus refs (`refs/<name>/`) and git config key (`<name>.remote`), the MCP server (`mcp__<name>__send`),
environment variables (`<NAME>_VERSION`), and the GitHub repo, Homebrew formula and npm package. A
leading digit breaks the environment variables (sh rejects `3SUM_VERSION`) and Go identifiers, so
"3sum", floated on 2026-10-30, would need a spelled-out prefix; it was also advised against as a sex
pun, and "3Sum" is a well-known coding-interview problem that owns its search results.

## Liked (Tyler, 2026-10-31)

- **Murmur.** A murmuration is a starling flock moving as one, and a murmur is quiet talk. The
  Homebrew name is free; npm's `murmur` is a dead 0.0.2 package (2022). Search is crowded by
  MurmurHash and Mumble's `murmur` server. **Three agent-messaging tools already use the name**
  (checked 2026-10-31):
  - `alexfrmn/murmur` ("Murmur Connect", 20★, pushed 2026-10-21): encrypted agent-to-agent
    messaging for Claude Code, Codex and any MCP client, across machines, over NATS, with Mac and
    Windows apps and an npm CLI. The same idea as t4, still active.
  - `slopus/murmur` (88★, pushed 2026-08-28): peer-to-peer encrypted messaging for agents and people.
  - `instavm/murmur` (39★, last pushed 2026-07-17): one person's agents on one machine, through a
    localhost daemon (`murmur`, `@instavm/murmur` on npm, MCP server `murmur`); agents poll rather
    than receive a turn. Its README lists cross-machine as a possible "V1.5".
  murmur.dev redirects to macroscope.com/murmur.
- **Gossip.** A gossip protocol is close to how the daemons spread state. It's a generic
  computer-science term, so its search results belong to tools like HashiCorp's `memberlist`; the
  Homebrew name is taken (a Nostr client) and npm's is a dead 0.0.1 package; and it means talk
  behind someone's back.

## Checked on 2026-10-31

Five models (DeepSeek V4 Pro, GLM-5.3 Flash, Muse Wonder, GPT-6 Luna, Kimi K3) each proposed eight
names and searched for collisions. What held up when checked:

- **Gaggle** is also Gaggle.net, K-12 student-surveillance software criticised by the EFF, which four
  of the five raised.
- **Foghorn** is also FogHorn Systems (edge AI), DNS proxies and an R package. One model's claim of
  a Foghorn MCP server for agents was false: the repo it cited doesn't exist.
- **Taken in this tool's own space:** Murmur (above), Muster (`musterhq`'s Muster Agent, a desktop
  app for Claude Code and Codex), Covey (covey.work, an agent platform; not checked by hand).
- **Clean at a GitHub search, worth a look:** Bugle (a call that rallies a group), Hubbub (many
  voices, and "hub" for GitHub; can read as chaos), Klaxon (an alarm; also a Kotlin JSON library).
  Also proposed: Skein (taken by `skein-js`, an agent runtime), Waddle, refchorus, Walkie,
  Partyline, Roster, Herald, Beacon (crowded).
