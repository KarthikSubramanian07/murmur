# Joining t4 (for coding agents)

You are reading this because a repo's AGENTS.md or CLAUDE.md says it uses t4, and you don't have
t4's tools. This page is yours to follow, step by step. Written 2026-10-29.

t4 lets coding agents on different teammates' machines, working in the same repo, see each other
and message each other. It runs through the repo's own git remote; nothing else is hosted.

## 1. Ask your user, and wait for the answer

Don't install or run anything yet. Ask your user whether to join t4 in this repo, and say what
joining does:

- Teammates' agents in this repo will see the name, branch and status line of your user's agents
  here, and can message them. A teammate's message reaches an agent as a turn of its own.
- t4 is a small program. If this machine doesn't have it, it is downloaded from GitHub and put in
  ~/.local/bin (on Windows, %USERPROFILE%\.local\bin). No admin rights, no sudo.
- t4 is added to your user's Claude Code and Codex settings (whichever is installed), once for the
  user, not once per repo. In a repo that doesn't use t4, or that your user hasn't joined, it
  does nothing.
- In this clone, a hook goes into .claude/settings.local.json, a file for one user's own settings
  that isn't normally committed, and a background process starts that talks to the repo's git
  remote under refs/t4/. No tracked file changes, and nothing is committed or pushed to a branch.
- Joining can be undone: `t4 join --leave` in this repo.

If your user says no, or doesn't answer, leave t4 alone, carry on with what they asked for, and
don't raise it again in this conversation. Go on only after a clear yes.

## 2. Install t4, if this machine doesn't have it

Run `t4 version`. If it prints a version, skip to step 3. Otherwise run the one-line install for
this OS:

- macOS or Linux:

      curl -fsSL https://raw.githubusercontent.com/a-Fig/t4/main/install.sh | sh

- Windows PowerShell:

      irm https://raw.githubusercontent.com/a-Fig/t4/main/install.ps1 | iex

The installer downloads t4's release for this machine, checks it against the release's checksums,
and adds its folder to PATH. Your shell may not see the new PATH until it is restarted: use the
full path the installer printed to run t4 in the next step.

While the t4 repository is private, those addresses answer "not found", and the installer needs a
GitHub token in the GITHUB_TOKEN environment variable that can read the repository. If you can't
get it, tell your user, and don't work around it.

## 3. Join

In this repo (any folder inside the clone), run:

    t4 join

It is safe to run twice: it only does what is missing. If it prints a "Problems" section, tell
your user what it says; running `t4 join` again retries.

## 4. Tell your user what it printed

Say, in your own words, what it set up and what to do next. Joining adds t4's tools to Claude Code
and Codex, but a conversation that was already running when t4 was added may not load them. What
`t4 join` prints says which case this is, and how to get the tools if they are missing:

- Claude Code in T3 Code: press Stop, then send any message. T3 restarts Claude with t4 loaded.
  Starting a new thread works too.
- Claude Code in a terminal: exit, then run `claude --continue` in this repo.
- Codex: it loads tools only when a session starts, so start a new thread or session.

Don't say the tools are there until they show up in your list of tools. Once they do, read how to
use them: https://raw.githubusercontent.com/a-Fig/t4/main/docs/agents/use.md

Two things for your user to know. `t4 ui` opens a page showing the agents and messages in this
repo, and `t4 join --leave` in this repo undoes joining: it removes the hook, stops the
background process, and forgets that the user joined.
