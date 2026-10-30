# t4: notes for Claude Code

t4 lets coding agents on different teammates' machines, in the same private repo, discover and
message each other, with git refs on GitHub as the transport. V1 exists (2026-10-26): one Go
binary, `cmd/t4`, with its packages in `internal/`; `go test ./...` runs it all, and every pull
request runs it on a runner on Tyler's home server, a machine his production work shares
(`docs/testing.md`). `docs/VISION.md` is the design and its reasons; read it before proposing
architecture, and change it when a decision changes. The name lives in `internal/brand`, so a rename is one file.

## Writing for whoever comes next

Docs and comments inform rather than direct: what is true, why, what it cost. Directions are
Tyler's rules, marked as his. Facts that can go stale carry a date. Each fact lives in one place:
this file loads into every session, so it stays short and points into `docs/` for detail.

## Words that mean two things

- **t4.** The repo, and the tool's working name. The product name is undecided; don't spread "t4"
  into identifiers that would be hard to rename.
- **session.** One agent conversation (a T3 thread, a Claude Code or Codex session). One device
  runs many.
- **device.** One clone's t4 daemon; the clone's worktrees share it, and a second clone on the
  same machine is a second device. It owns one ref, `refs/t4/mesh/<deviceId>`.
- **mesh, bus.** The set of `refs/t4/*` refs on the remote that carry events.

## Traps

- **Never touch the working tree or index when writing to the bus.** Humans and agents are editing
  that checkout at the same moment. Bus writes use plumbing (`commit-tree`, `push <sha>:<ref>`);
  a checkout of a bus ref, even briefly, would wreck someone's work.
- On Windows, Git Bash rewrites POSIX-looking arguments into Windows paths unless
  `MSYS_NO_PATHCONV=1` is set; refs like `refs/t4/...` are safe, absolute paths are not.

## Issue labels

Every issue carries at least a kind label.

- `bug` / `Feature` / `Task` / `question`: the kind, spelled exactly so. `question` is an open
  design question.
- `priority: now` / `soon` / `future`: open issues only, one of them.
- `PR needed` / `non-PR needed`: where the work lives. `non-PR needed` means some of it is done by
  hand: a decision, an account, a test in the world.
- `from: <Model>`: see Workflow.

Why an issue closed is its `state_reason` (`completed` / `not_planned` / `duplicate`), not a label.
Tyler's rule: no new label without a reason.

## Workflow

Tyler's rules:

- Commit messages and PR bodies explain *why*.
- Every PR closes at least one issue, and its body starts with only its `Closes #N` lines; work no
  issue covers gets one filed first.
- PR bodies follow `.github/pull_request_template.md`. GitHub pre-fills it only in the web UI, so an
  agent opening a PR through `gh` writes those sections itself.
- Every code PR gets an Opus or sol review before it is handed over; the PR body says which
  commits it covered. Doc-only PRs need none.
- Issues a model files on its own initiative get `(from <model>)` in the title and the matching
  `from: <Model>` label. An issue Tyler asks for is `from: Tyler`, even though a model typed it.
