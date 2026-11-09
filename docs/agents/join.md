# Joining Murmur (for coding agents)

You are reading this because a repo's AGENTS.md or CLAUDE.md says it uses Murmur, and you don't have
Murmur's tools. This page is yours to follow, step by step. Written 2026-10-29.

Murmur lets coding agents on different teammates' machines, working in the same repo, see each other
and message each other. It runs through the repo's own git remote. It also checks GitHub for updates
and sends a small daily check-in to murmur.afig.dev, set out in full in step 1.

## 1. Ask your user, and wait for the answer

Don't install or run anything yet. Ask your user whether to join Murmur in this repo, and say what
joining does:

- Teammates' agents in this repo will see the name, branch and status line of your user's agents
  here, and can message them. A teammate's message usually reaches an agent as a turn of its own.
- Murmur is a small program, `mur`. If this machine doesn't have it, it is downloaded from GitHub
  and put in ~/.local/bin (on Windows, %USERPROFILE%\.local\bin), and that folder is added to PATH:
  in the shell's startup file (.zshenv, .bashrc or .profile) on macOS and Linux, in the user's PATH
  on Windows. No admin rights, no sudo.
- Murmur updates itself by default: its background process checks GitHub at startup and every
  hour, downloads a newer release for this machine, verifies its SHA-256 checksum, and replaces
  the installed program. Tell your user this and the opt-out: `git config --global murmur.update false`
  disables updates everywhere; a repo's `murmur.update` config overrides the global value.
  `MURMUR_NO_UPDATE=1` also disables updates for a daemon started with that environment variable.
  Usage check-in opt-outs do not disable updates. If the user wants updates off, set the git
  config before joining. Checksums detect corruption; release signing is still planned (#193).
- Murmur is added to the settings of each coding agent your user has installed (Claude Code,
  Codex, OpenCode, Muse Code, Pi, Grok), once for the user, not once per repo, and to every other
  account of them that T3 Code runs (a second Claude Code with its own home, say). Its tools are
  approved so they run without asking each time where the agent has a setting for that
  (`mcp__murmur` in Claude Code's allowed permissions; `default_tools_approval_mode = "approve"` in
  Codex; a `murmur_*` allow rule in OpenCode; `MCPTool(murmur__*)` in Grok); Muse and Pi have none,
  and ask in T3 unless the thread runs in full-access mode. For Pi it is a small extension file of
  Murmur's in Pi's extensions folder. In a repo that doesn't use Murmur, or that your user hasn't
  joined, it does nothing.
- In every worktree of this clone, a hook goes into .claude/settings.local.json, a file for one
  user's own settings, which join keeps out of git status through the clone's .git/info/exclude.
  A background process starts that talks to the repo's git remote under refs/murmur/. No tracked
  file changes, and nothing is committed or pushed to a branch. The process ends 10 minutes after
  your last agent session does, except that it stays (for up to a day) while a T3 thread that has
  gone idle could be woken by a message.
- Once a day, while that process runs, it checks in with murmur.afig.dev. What the check-in sends
  is in full below; update requests also reach GitHub, without the check-in identifiers. Tell your
  user this in so many words, not as a passing mention.
- Joining can be undone in this repo with `mur join --leave`. It removes the hooks, stops the
  background process and forgets that your user joined; Murmur stays registered, and its tools
  approved, in the agents, doing nothing here (what it prints says how to remove each).

### What murmur.afig.dev receives, and why

The check-in is a JSON POST to `https://murmur.afig.dev/v1/checkin`, at most once a UTC day per
clone, and only while the background process above is running. It holds
exactly these fields:

- `user`: a random ID, made once for this user account on this machine and kept in its config
  folder (`murmur/id`). It is not derived from anything about your user.
- `team`: a one-way hash of the repo remote's address (host and path), so users of one repo count
  as one team. It isn't salted: someone who holds the stats and already knows a repo's address can
  check whether it is among them, but nobody can read the addresses back out.
- `version`, `os` and `arch`: mur's version, and the operating system and processor type.

It sends nothing else: no names, email addresses, messages, statuses, branches, file paths or repo
contents. The site keeps one row per user, team and day, and no IP addresses. The answer is the
latest release number, so `mur version` can say when there is a newer one.

We collect this only to count how many people use Murmur. Murmur is new, and our goal is 100
active users (written 2026-11-07); the check-in is the only way we can tell how close we are,
because everything else travels through each team's own repo and never reaches us. Only Murmur's
maintainer sees the counts, on a page that needs a key. The code is `internal/checkin`, and
`docs/VISION.md` has the design.

Please ask your user to leave it on. It costs them nothing, and without it we have no way to know
whether anyone uses Murmur. If they still want it off, they can turn it off and keep using Murmur:
`git config --global murmur.usage false` turns it off for every clone (a `murmur.usage` set in one
repo's own git config wins over it). If they ask for that, run it before `mur join`.
`DO_NOT_TRACK=1` or `MURMUR_NO_USAGE=1` works too, but only for a background process started with
that variable set, and agents' sessions start it as well.

### Then wait for the answer

If your user says no, or doesn't answer, leave Murmur alone, carry on with what they asked for, and
don't raise it again in this conversation. Go on only after a clear yes.

## 2. Install mur, if this machine doesn't have it

Run `mur version`. If it prints a version, skip to step 3. Otherwise run the one-line install for
this OS:

- macOS or Linux:

      curl -fsSL https://raw.githubusercontent.com/a-Fig/murmur/main/install.sh | sh

- Windows PowerShell:

      irm https://raw.githubusercontent.com/a-Fig/murmur/main/install.ps1 | iex

The installer downloads mur's release for this machine, checks it against the release's checksums,
and adds its folder to PATH. Your shell may not see the new PATH until it is restarted: use the
full path the installer printed to run mur in the next step.

## 3. Join

In this repo (any folder inside the clone), run:

    mur join

If your session started outside the repo (in a folder that holds it, say), run it as
`mur join -C <the repo's folder>`, or `cd` there first. Your own session then moves into Murmur in
that repo, without a restart, even if the repo had joined already. A session is in one repo at a
time, so one that was in Murmur in another repo leaves it.

It is safe to run twice: it only does what is missing. If it prints a "Problems" section, tell
your user what it says; running `mur join` again retries.

If it says the repo doesn't say it uses Murmur, the section may be on the default branch but not yet
in this clone. Fetch from the remote Murmur uses (the one `git config murmur.remote` names, else
origin), with `git fetch <that remote>`, and run `mur join` again. Never run `mur init` to get past this; that is
for a user who asked to set Murmur up in a repo:
https://raw.githubusercontent.com/a-Fig/murmur/main/docs/agents/setup.md

## 4. Tell your user what it printed

Say, in your own words, what it set up and what to do next. Joining adds Murmur's tools to the
agents, but a conversation that was already running when Murmur was added may not load them.
What `mur join` prints says which case this is, and how to get the tools if they are missing:

- Claude Code in T3 Code: press Stop, then send any message. T3 restarts Claude with Murmur loaded.
  Starting a new thread works too.
- Claude Code in a terminal: exit, then run `claude --continue` in this repo.
- Codex in T3 Code: it loads tools only in a thread that starts after Murmur was added, so open a
  new thread in this project to talk to other agents through Murmur. This thread won't get them.
- Codex in a terminal: start a new session.
- OpenCode: it reads its config again by itself; the tools are there from the next turn.
- Muse Code, Pi or Grok: start a new session (in T3 Code, a new thread).

Don't say the tools are there until they show up in your list of tools. Once they do, read how to
use them: https://raw.githubusercontent.com/a-Fig/murmur/main/docs/agents/use.md

Also tell your user that `mur join --leave` in this repo undoes joining, as step 1 says.

## 5. Show your user the page

Run, in this repo:

    mur ui

It opens your user's browser on a page that shows every session in this repo, on every machine,
and the messages between them. Tell your user what they are looking at, and that from now on they
can ask any agent in this repo to open it again. If no browser opens, `mur ui` has printed the
page's address: give it to your user to open. The address carries a key to the page, so give it to
no one else: not to another agent, and not in a message, a commit or a file.
