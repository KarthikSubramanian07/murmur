# Setting Murmur up in a repo (for coding agents)

You are reading this because your user asked you to set Murmur up in a repo that doesn't use it
yet. This page is yours to follow, step by step. Written 2026-11-01.

Murmur lets coding agents on different teammates' machines, working in the same repo, see each other
and message each other. It runs through the repo's own git remote; nothing else is hosted. Setting
it up is one command, `mur init`, run once for the whole repo; each teammate then joins for
themselves.

First look at AGENTS.md and CLAUDE.md at the top of the repo. If either has a section that starts
with `<!-- murmur:start -->`, the repo is already set up: follow
https://raw.githubusercontent.com/a-Fig/murmur/main/docs/agents/join.md instead. (To bring an older
section up to date, see the last item at the end of this page.)

## 1. Ask your user, and wait for the answer

Don't install or run anything yet. Find the repo's default branch (`git ls-remote --symref origin
HEAD` names it; usually main), then ask your user whether to set Murmur up here, and say what that
does:

- `mur init` pushes one commit to that branch on the remote, in the user's name. It adds a short
  section to AGENTS.md and CLAUDE.md (creating either if it is missing) that says the repo uses
  Murmur. Every teammate's agent reads those files, so each will see the section.
- Nothing is installed on teammates' machines, and Murmur does nothing for anyone until they join.
  A teammate's agent that reads the section asks its user whether to join.
- If the remote refuses the push (a protected branch, say), the commit goes to a new branch,
  murmur-init, for a pull request that someone allowed to merge into the default branch has to
  merge.
- Then you join for your user, which is what join.md describes.

If your user says no, or doesn't answer, leave Murmur alone and carry on with what they asked for.
Go on only after a clear yes.

## 2. Install mur, if this machine doesn't have it

Run `mur version`. If it prints a version, skip to step 3. Otherwise run the one-line install for
this OS:

- macOS or Linux:

      curl -fsSL https://raw.githubusercontent.com/a-Fig/murmur/main/install.sh | sh

- Windows PowerShell:

      irm https://raw.githubusercontent.com/a-Fig/murmur/main/install.ps1 | iex

Your shell may not see the new PATH until it is restarted: use the full path the installer printed.
If the addresses answer "not found", step 2 of join.md says why.

## 3. Run init

In the repo (any folder inside the clone), run:

    mur init

Tell your user what it printed: the commit it pushed (by its short sha) and to which branch. If the
push was refused, give them the pull-request link it printed (or the branch name, when the remote
isn't on GitHub) and say that someone with the right to merge into the default branch has to merge
it; until then teammates' agents don't see the section.

init leaves this checkout alone unless it is on the default branch, with no commits of its own and
no changes to those files; then it moves it to the new commit. A checkout on another branch gets the
section when the default branch is merged into it, and in this clone Murmur counts it as set up
meanwhile.

`mur init --no-push` only edits the files here, for the user to commit themselves. Use it only if
your user asks for that.

## 4. Join

Follow join.md from step 3: run `mur join`, tell your user what it printed, including how to get
the tools in this conversation, and that `mur ui` shows the agents and messages.

## What to tell your user about teammates

- Each needs push access to the repo: Murmur's messages travel as git refs on its remote.
- They don't run anything themselves. Their agent reads the section and asks them whether to join,
  and if they say yes, it installs mur and joins.
- What Murmur is, for anyone who asks: https://murmur.afig.dev

## When something goes wrong

Tell your user what mur printed and what they can do. Don't work around it: no force pushes, no
editing other files to get past an error, and never join on a teammate's behalf.

- "not inside a git repository": the folder isn't in a git clone. Ask which repo they meant.
- "this clone has no remote": the clone has no `origin` (or the remote that `git config
  murmur.remote` names). Murmur needs one; the user adds it.
- git older than 2.31 (`git --version`): mur needs 2.31 or later; the user updates git.
- "git doesn't know who you are": the commit is in the user's name, so they set `git config
  user.name` and `user.email`.
- The push failed for lack of permission: the user needs push access, or someone who has it runs
  init.
- The push was refused and a murmur-init branch is already there without the section: someone else
  made that branch. The user deletes or renames it, then you run init again.
- An older Murmur section is there: running init again rewrites it to the current one, with a
  commit like the first, and removes the Murmur entries an older init put in `.mcp.json` and
  `.claude/settings.json`. A section whose `<!-- murmur:end -->` line is gone was edited by hand,
  and init leaves it; the user restores that line or deletes the section, then you run init again.
