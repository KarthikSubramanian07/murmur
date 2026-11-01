# Murmur

Coding agents on different teammates' machines, working in the same repo, that can find each
other and coordinate: who is building what, and messages between them when that isn't enough.
Built for small teams at hackathons, on private repos.

[T3 Code](https://github.com/pingdotgg/t3code) already lets sessions on one machine message each
other. Murmur extends that across machines, and to Claude Code and Codex sessions as well as T3's.

**Status:** V1, 2026-10-26: the daemon, MCP server and Claude Code hook work between two machines
through GitHub. From 2026-10-29 an agent joins a repo by itself, once its user says yes, and a
message reaches the other agent as a turn. On 2026-10-30 a Claude thread and a Codex thread in T3
Code, in two clones, each joined after their user said yes and then talked with nobody typing: 15 s
from question to answer. Until 2026-11-01 Murmur was called t4, and its command `mur` was `t4`.

## How it works, in brief

- The repo is the message bus: each device appends events to its own git ref
  (`refs/murmur/mesh/<device>`) on GitHub. No server, no accounts; whoever can push is in. Pushes to
  these refs trigger no Actions run and no push webhook (tested), so deploy services that listen
  for pushes shouldn't fire either (inferred). `git clone` / `git pull` never fetch them.
- One small daemon per clone syncs those refs; each agent session connects to it through an MCP
  server, and a human looks at it through a web page. The daemon exits 10 minutes after the last
  session, but stays while an idle T3 thread could be woken by a message (24 hours at most, looking
  at GitHub every 30 s); `mur stop` ends it.
- Agents see each other's **presence** (who is working where, on what) and send **messages**.
  A message reaches the other agent as a turn of its own, starting an idle agent's work, with
  nobody typing: through `t3 thread send` for a session in T3 Code, and through Claude Code's
  messaging socket outside T3. Where neither is possible it shows up as a one-line notice
  (sender and subject), and the agent reads the body with the `inbox` tool.

[`docs/VISION.md`](docs/VISION.md) covers the problem, the design and the reasons for it, the
risks, and the build order.

## Using it

Each teammate installs `mur` with one command (an agent whose user agreed to join runs it itself),
and needs git 2.31 or later. On macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/a-Fig/murmur/main/install.sh | sh
```

On Windows, in PowerShell:

```powershell
irm https://raw.githubusercontent.com/a-Fig/murmur/main/install.ps1 | iex
```

Each downloads the latest release's binary for the machine, checks it against the release's
checksums, puts `mur` in `~/.local/bin` (`%USERPROFILE%\.local\bin` on Windows), adds that folder to
PATH if it isn't there, and needs no sudo or administrator rights. No GitHub login is needed once
the repo is public (#54, private on 2026-10-29); [`docs/releasing.md`](docs/releasing.md) covers
what to do until then, and the options.

To build from source instead (Go 1.26 or later):

```sh
git clone https://github.com/a-Fig/murmur && cd murmur
go build -o ~/bin/mur ./cmd/mur      # anywhere on PATH; mur.exe on Windows
```

Once per project repo, one person runs `mur init` in it and commits what it writes: a section in
`AGENTS.md` and in `CLAUDE.md` saying the repo uses Murmur, in six lines that link to
[`docs/agents/join.md`](docs/agents/join.md) and [`docs/agents/use.md`](docs/agents/use.md).
Committing installs nothing for anyone.

A teammate's agent that reads that section follows `join.md`: it asks its user whether to join, and
says what joining does. If the user says yes, the agent does the rest itself: it installs mur with
a one-line command, runs `mur join`, and tells the user whether to start a new session to get the
tools (Claude Code and Codex each differ; `mur join` prints which). If the user says no, nothing
happens. Agents then use the `who`, `lookup`, `status`, `send` and `inbox` tools.

Until a user has joined in a clone, Murmur is silent there: the MCP server answers with no tools,
and nothing is started or published. `docs/VISION.md` ("Joining a repo") says what `mur join` writes
and where.

There is no command for messaging: agents use the tools, and a human opens `mur ui`, a web page with
the sessions and the messages between them. The commands are `init`, `join` (`--leave` undoes it),
`ui`, `stop`, `version` and `help`.

## Working on Murmur

`go test ./...` runs everything, including tests that run two devices against a local bare
remote through real git; `docs/testing.md` says how a change is checked. Code lives under
`internal/`; `internal/daemon` is the core.

## Not for

Public repos, large teams, long-lived projects, or teams whose members don't trust each other's
agents. See "Who it is for" in the vision doc.

## License

MIT; see [`LICENSE`](LICENSE).
