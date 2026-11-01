# Joining Murmur (for coding agents)

You are reading this because a repo's AGENTS.md or CLAUDE.md says it uses Murmur, and you don't have
Murmur's tools. This page is yours to follow, step by step. Written 2026-10-29.

Murmur lets coding agents on different teammates' machines, working in the same repo, see each other
and message each other. It runs through the repo's own git remote; nothing else is hosted.

## 1. Ask your user, and wait for the answer

Don't install or run anything yet. Ask your user whether to join Murmur in this repo, and say what
joining does:

- Teammates' agents in this repo will see the name, branch and status line of your user's agents
  here, and can message them. A teammate's message usually reaches an agent as a turn of its own.
- Murmur is a small program, `mur`. If this machine doesn't have it, it is downloaded from GitHub
  and put in ~/.local/bin (on Windows, %USERPROFILE%\.local\bin), and that folder is added to PATH:
  in the shell's startup file (.zshenv, .bashrc or .profile) on macOS and Linux, in the user's PATH
  on Windows. No admin rights, no sudo.
- Murmur is added to your user's Claude Code and Codex settings (whichever is installed), once for
  the user, not once per repo, with its tools approved so they run without asking each time
  (`mcp__murmur` in Claude Code's allowed permissions; `default_tools_approval_mode = "approve"` in
  Codex). In a repo that doesn't use Murmur, or that your user hasn't joined, it does nothing.
- In every worktree of this clone, a hook goes into .claude/settings.local.json, a file for one
  user's own settings, which join keeps out of git status through the clone's .git/info/exclude.
  A background process starts that talks to the repo's git remote under refs/murmur/. No tracked
  file changes, and nothing is committed or pushed to a branch. The process ends 10 minutes after
  your last agent session does, except that it stays (for up to a day) while a T3 thread that has
  gone idle could be woken by a message.
- Joining can be undone in this repo with `mur join --leave`. It removes the hooks, stops the
  background process and forgets that your user joined; Murmur stays registered, and its tools
  approved, in Claude Code and Codex, doing nothing here (what it prints says how to remove those).

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

While the Murmur repository is private, those addresses answer "not found", and the installer needs
a GitHub token in the GITHUB_TOKEN environment variable that can read the repository. If you can't
get it, tell your user, and don't work around it.

## 3. Join

In this repo (any folder inside the clone), run:

    mur join

It is safe to run twice: it only does what is missing. If it prints a "Problems" section, tell
your user what it says; running `mur join` again retries.

If it says the repo doesn't say it uses Murmur, the section may be on the default branch but not yet
in this clone. Fetch from the remote Murmur uses (the one `git config murmur.remote` names, else
origin), with `git fetch <that remote>`, and run `mur join` again. Never run `mur init` to get past this; that is
for a user who asked to set Murmur up in a repo:
https://raw.githubusercontent.com/a-Fig/murmur/main/docs/agents/setup.md

## 4. Tell your user what it printed

Say, in your own words, what it set up and what to do next. Joining adds Murmur's tools to Claude
Code and Codex, but a conversation that was already running when Murmur was added may not load them.
What `mur join` prints says which case this is, and how to get the tools if they are missing:

- Claude Code in T3 Code: press Stop, then send any message. T3 restarts Claude with Murmur loaded.
  Starting a new thread works too.
- Claude Code in a terminal: exit, then run `claude --continue` in this repo.
- Codex in T3 Code: it loads tools only in a thread that starts after Murmur was added, so open a
  new thread in this project to talk to other agents through Murmur. This thread won't get them.
- Codex in a terminal: start a new session.

Don't say the tools are there until they show up in your list of tools. Once they do, read how to
use them: https://raw.githubusercontent.com/a-Fig/murmur/main/docs/agents/use.md

Two things for your user to know. `mur ui` opens a page showing the agents and messages in this
repo, and `mur join --leave` in this repo undoes joining, as step 1 says.
