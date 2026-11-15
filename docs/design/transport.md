# Git as the transport

## Git is the transport, GitHub the hub

Every teammate already has credentials and push access to the repo, so the repo carries the
messages. No server, no accounts, no extra auth: whoever can push is in. (Outside the repo it checks GitHub for updates and, for a public repo only, sends us the repo's
address once; see Updating and Counting use.)

- Each device appends events to **its own ref**, `refs/murmur/mesh/<deviceId>`: one empty commit per
  event, the payload in the commit message (a readable subject, a JSON body). One writer per ref
  means pushes never race. A message to another session sits in the sender's log, addressed to it.
- Per-device logs can't settle a race: two devices could each take the same session name (#20)
  within one sync, each seeing no rival. What must be settled before anyone acts on it needs
  one shared ref, which GitHub orders. A push to a ref is atomic, and with a lease
  (`--force-with-lease`) it is refused unless the ref still holds what the pusher last fetched,
  which makes the ref a compare-and-swap. A refused pusher fetches and decides again with what it
  hadn't seen. V1's claims worked this way; in its test through GitHub (2026-10-26), two machines
  pushed at the same instant three times, and each time exactly one push landed. Session names
  work this way too, on `refs/murmur/names` (see Session names).
- Presence is separate: `refs/murmur/presence/<deviceId>`, force-pushed with the device's current
  state when it changes and on a heartbeat about every 2 minutes. Heartbeats would otherwise flood
  the event log. A presence unchanged for 6 minutes is stale: its device's sessions may not get
  what is sent to them, and `who` and `send` say so.
- A reader measures staleness from when it saw a presence change, on its own monotonic clock, and
  only up to its own last successful sync, so a reader that was itself asleep or offline doesn't
  take everyone else for gone. Only a presence it has never seen change is dated by the writer's
  clock, from when the tip commit was written and never later than now. A daemon that has just
  started sees every device's ref as new, so dating them from now showed devices dead for hours
  as alive for the first 6 minutes, which is when a new session is most likely to ask `who`
  (seen live, 2026-10-30). A writer whose clock runs more than 6 minutes slow looks stale until
  its next heartbeat, at most 2.4 minutes, when the reader sees the presence change and times it
  itself.
- Custom refs, not branches, so CI and preview deploys don't fire on every message, and
  `git clone` / `git pull` never fetch them. git-bug uses `refs/bugs/*` on GitHub the same way.
  Tested on this repo on 2026-10-26: a push to `refs/t4/*` produced no `push` webhook delivery
  and no Actions run, even when the pushed commit contained a workflow that runs on every push;
  the same commit pushed to a branch produced both. Vercel and Netlify deploy from GitHub's push
  webhooks, so they have nothing to react to (inferred, not tested with either).
- Writes use git plumbing (`commit-tree`, `push <sha>:<ref>`) and never touch the working tree or
  index, where humans and agents are working at the same moment.
- A device pushes one ref at a time (#141, 2026-11-01). Its presence, its log and the names
  registry were each pushed from a goroutine of their own, so a session starting set off three
  pushes in the same second (its name, its start on the log, the presence) and one stopping two.
  GitHub refused one of them now and then with `[remote rejected] (failed)`, which is no lease
  refusal, and a refused presence waited 15 s for its retry. In one laptop's log of 2026-11-01 all
  9 refusals (7 presence, 2 names) came within seconds of a session starting or stopping, about one
  such moment in five with the network up, and none came while presence was pushed alone on its
  heartbeat, over several hours. That is inferred from the timing, not reproduced against GitHub; a
  bare repo on disk takes concurrent pushes without complaint. Now a push waits for the one before
  it, so a `send` made while presence is being pushed waits about a second more.
- Pushes from different devices still meet on GitHub, and it refuses them the same way. Two
  devices whose daemons started within a second of each other, so that their heartbeats fell
  together, logged 17 presence pushes refused with `(failed)` in 45 minutes, taking turns, and one
  with `(failure)` (2026-11-09). Two clients pushing to the same repository at the same moment were
  refused 2 times in 20 (`(failed)`, `(fatal error in commit_refs)`), and 4 s apart, never. A
  refused message failed the agent's `send`. Since 2026-11-10 `bus.Push` sends a push refused for
  one of those reasons again, a few times, and the look at the remote ref it takes after any failed
  push keeps a try that landed anyway from being written twice. Heartbeats are spread by 20% either
  way, so devices that started together drift apart.
- Of the pushes a session's start sets off, the presence goes first (2026-11-10). The start on the
  log and the assignment of a name from the pool were ready within milliseconds and took the turn
  first; the presence, which waits 300 ms to gather a session's first changes, went last. Over
  GitHub it began 4.05 s after the session started (median, 2026-11-09), and only the presence
  shows the session in other devices' `who`. Now the other two wait until a presence listing the
  session has been pushed, or has failed. A presence can carry a pool name before its assignment
  lands, which is safe: the name is already set aside for that device, and readers take names from
  presence (see Session names). A session that found the pool empty doesn't hold back the push
  that gives it its name. Against a local bare remote that holds each push for 1.3 s, the presence
  landed 1.86 s after the start instead of 4.71 s (median of 10), and the start on the log 3.45 s
  instead of 1.53 s. With no hold, the presence landed at about the same time (0.51 s against
  0.56 s), and the other two about 0.6 s later.
- Bus commits carry git's empty tree, so they hold no files and fetching them brings nothing else
  along. The daemon pushes them with `--no-verify` and doesn't sign them: a `pre-push` hook that
  runs the test suite, or a signing prompt, on every message would make the bus unusable. These
  commits only ever go to `refs/murmur/*`, never to a branch.
- Events carry no signature of the device's own, so a device can write events that claim to come
  from another (#155). Only accounts with push access can write `refs/murmur/*` (a fork can't), and
  any of them could already push code. A signature would stop one of them posing as another
  teammate's device, and little else: a message gets no authority from who sent it, and an agent
  can inject a prompt under its own name. A full version was built and set aside on 2026-11-03
  (Tyler's decision): Ed25519 keys per device, pinned on first sight, with replay checks, about 860
  lines, on branch `bus-signing` (PR #159, closed unmerged). Since #156 presence is sealed with that
  branch's seal, because it carries the key a device's teammates encrypt to (next section).
- beads (task tracking synced through `refs/dolt/data` on GitHub) shows custom refs hold up at
  scale, and two of its bugs are warnings: `git push --mirror` from an ordinary clone deletes
  custom refs on the remote (beads #5266), and checking an internal branch out in the user's repo
  wiped tracked files (beads #6484).
- `mesh/<deviceId>`, `presence/<deviceId>` and `names` are the only names under `refs/murmur/`
  Murmur reads.
  Device IDs are lowercase letters, digits and dashes, never a name Windows can't hold as a file.
  Anything else there is left on the remote unfetched. Anyone who can push can write any of these
  refs, so a reader holds what it reads to what Murmur writes and builds over junk rather than on
  it. The remote refuses a ref whose name clashes with another's, so a daemon deletes the refs that
  would stop Murmur's own being pushed: those under its own names (`presence/<deviceId>/...`,
  `mesh/<deviceId>/...`, `names/...`), and `refs/murmur/mesh` or `refs/murmur/presence` as refs
  themselves. Murmur writes nothing at any of them (found by the Opus reviews of V1, #8,
  2026-10-28).
- A reader holds what it reads to a size, too (2026-11-09). Git keeps commits compressed, so a
  quick fetch can bring a message of any size: one of 64 MB packed to 64 KB, and reading it whole
  took 448 MB. A commit over 1 MB on a log or a presence, or 32 MB on the names registry (a
  snapshot lists every name, about 45 bytes each), is passed over unread, as junk is, and the
  cursor moves past it; git gives the size without the read. Nothing Murmur writes comes near
  either. A log is read 500 commits at a time, with the daemon's lock let go between pages and the
  state file saved once a page, not once per tags event, so a device back after days away doesn't
  stall the sessions, the hook and the page while it catches up. It is read along first parents:
  Murmur writes every log as a chain, and a page's last commit, the cursor a cut-short read goes on
  from, stands for all before it only on a chain. A merge someone wrote by hand would otherwise have
  its other side read again, and its messages delivered twice.
- Reading: poll `git ls-remote origin 'refs/murmur/*'`, which is cheap; fetch only when a SHA has
  changed. How often is "Speed", below (3 s around a conversation; 10 s before 2026-10-29). A message takes about 6–8 s from a `send` to the
  receiving agent's turn ("Delivering a message as a turn", below); V1, polling every 10 s, took
  about 12 s from a Windows laptop to a macOS machine (2026-10-26), 2 s of it the send.
- A device is one clone's daemon. Worktrees of a clone share its `.git`, so they share its daemon;
  a second clone on the same machine is a second device. The device ID is the hostname plus four
  random hex digits (`tyler-laptop-3f2a`), kept with the daemon's other state in `.git/murmur/`,
  which no commit or checkout ever touches. What people call it is "Device nicknames", below. (An earlier draft grouped repos by root commit;
  per-clone daemons don't need that.)

- **Speed.** The daemon polls the remote every 3 s around a conversation, not 10 s (2026-10-29), and
  hands a message over the moment it is fetched. Measured between two T3 threads on one laptop through GitHub over HTTPS:
  about 6–8 s from an agent's `send` to the other thread's turn starting (a push, 1.2 s; up to
  one poll; `ls-remote`, 1.2 s; a fetch; `t3 thread send`, 1.1 s), and a question answered by
  another agent's reply in about 19 s. GitHub documents no limit on git reads, and a test of 63
  `ls-remote` requests about 2 s apart drew none, which is a short test, not evidence about
  hours of them from every device of a team. A poll that fails waits twice as long each time, to
  at most a minute (or 20 polls, if Poll is shorter), and goes back to its usual rate when it works.
  A fetch offers the server only the clone's bus commits as what it already has
  (`--negotiation-tip=refs/murmur/*`, 2026-11-10). By default git offers every local ref's
  commits, and for a presence commit, which has no parent, the server is never ready, so the clone
  listed the user's whole history: with 350 commits of its own, 300 of them unpushed, 6 requests
  offering 377 commits, against 2 offering 18. Over GitHub, before the change, a fetch with a
  presence ref in it took 0.93 s and one without 0.45 s (2026-11-09); nothing was measured over
  GitHub after it. The cost is junk: a commit built on a branch and pushed onto a bus ref brings
  the branch's whole history (540 KB against 12 KB for a 200-commit branch), since the server
  isn't told the clone has it, and on a large repo the slow fetch below takes it. Offering the
  remote's `HEAD` too would avoid that, but cost a presence fetch a request more, and a mesh fetch
  too when the branch is newer than the log, so it was left out.
- **A ref too slow to fetch (2026-11-08, #15).** Anyone with push access can put a large object on
  a bus ref, on purpose or with a mistaken push. A sync has a minute, and a fetch that used it up
  failed that sync and the next, for as long as the object stayed. So each fetch in a sync now has
  15 s (`FetchLimit`). A ref that runs past it on its own is fetched outside the syncs, one at a time,
  for up to 10 minutes (`SlowFetchLimit`), waiting 1 minute after a failure and twice as long after
  each one that follows, to at most an hour. The next sync reads what that fetch brought, rather than
  fetching the ref again, which on a link slow enough to run past the limit every time would mean
  never reading it (#223's first version did this). The cost that remains: an object that does finish
  downloading stays in the clone until a gc removes it, since git can't cap what a fetch brings. `git
  fetch --filter` would, but one such fetch makes the user's own clone a partial clone
  (`remote.origin.promisor`), for their branches too. On Windows a fetch cut off is killed, which leaves its
  `tmp_pack_*` behind, one per try (seen 2026-11-08), so the daemon removes those that have gone
  unwritten for 10 minutes.
- **How often it polls (2026-10-30, #93).** Tyler: agents don't need real-time communication, and
  4–7 s a hop is fine. So a daemon polls fast only when a reply is likely, and a session left open
  for hours, which would cost 1,200 `ls-remote` calls an hour at 3 s, costs 240.
  - *Fast, every 3 s (`Poll`), for 3 minutes (`FastFor`)* after this device sends a message, or
    queues one for a session on this device or hands one over to it, or a session starts here. A reply
    usually comes within a minute or two (the other agent's thinking time plus two hops), and each
    move restarts the 3 minutes.
  - *Otherwise, with a session live or a page open,* the wait doubles after each poll that follows
    FastFor's end: 3 s, 6, 12, up to 15 s (`PollCap`). Doubling reaches the cap in three polls; a
    first message of a conversation then waits at most 15 s, plus the handoff.
  - *Quiet:* with no session live and no page open the poll is every 30 s (`QuietPoll`, #87),
    whatever was said lately. A session registering, or a page opening, makes it look at once, and
    a session starting is itself a move, so it polls fast for the 3 minutes.
  - *Not moves:* presence heartbeats and status changes, which arrive all the time from active
    teammates and don't predict a reply, and other devices' messages for sessions elsewhere.
  - A failed poll keeps the back-off above, from whatever the rate was.
  - All four numbers are options, so a test can shrink them.
