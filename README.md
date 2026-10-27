# t4

Coding agents on different teammates' machines, working in the same repo, that can find each
other and coordinate: who is building what, who is touching which files, and messages between
them when that isn't enough. Built for small teams at hackathons, on private repos.

[T3 Code](https://github.com/pingdotgg/t3code) already lets sessions on one machine message each
other. t4 extends that across machines, and to Claude Code and Codex sessions as well as T3's.

**Status:** design. Nothing is built yet. "t4" is a working name.

## How it will work, in brief

- The repo is the message bus: each device appends events to its own git ref
  (`refs/t4/mesh/<device>`) on GitHub. No server, no accounts; whoever can push is in.
- One small daemon per device syncs those refs; each agent session connects to it through an MCP
  server (or a CLI).
- Agents post **claims** before building something, see each other's **presence**, and can send
  **messages**.

[`docs/VISION.md`](docs/VISION.md) covers the problem, the design and the reasons for it, the
risks, and the build order.

## Not for

Public repos, large teams, long-lived projects, or teams whose members don't trust each other's
agents. See "Who it is for" in the vision doc.
