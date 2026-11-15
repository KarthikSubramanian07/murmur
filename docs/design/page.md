# The page for humans

Added 2026-10-29 (#22). A human sees what the agents are doing, and the messages between them, in
a browser. The page is `internal/web`: static files embedded in the binary and a small JSON API,
which the daemon serves and answers from what it already holds in memory, so a poll makes no git
call.

- **Served by the daemon** on 127.0.0.1, on a random port of its own: the sessions' port speaks the
  daemon's protocol, and the page speaks HTTP to a browser. The address is `web` in
  `.git/murmur/daemon.json`, with a token of its own (`pageToken`, #42): the sessions' token lets
  its holder send as this device, and the page's address, which carries its token, reaches the
  browser's history and extensions. A daemon that can't open the port logs that and keeps running
  without the page; `mur ui` then says there is none.
- **Opened by `mur ui`**, which starts the daemon if it isn't running, opens the default browser
  and prints the address. The page's token is in the address's fragment (`#token=`), which a browser
  keeps from the server; the page reads it and sends it with each request. `mur ui --no-open`
  prints the address and opens nothing.
- **Switching repos** (2026-11-09, #348). The sidebar's header names the repo, and opens this
  machine's other clones that joined; picking one moves the tab to that clone's page. Each clone's
  daemon serves its own page, on its own port with its own token, and none knows the others, so
  they are written down: a per-user record, `clones` in the user's config directory, one file per
  clone so that writers never need a lock (`internal/clones`). `mur join` adds a clone, `mur leave`
  takes it off, and a daemon adds its own as it starts, which takes in clones that joined before the
  record. Daemons finding each other was set aside: the picker has to offer a clone whose daemon
  isn't running, and start it. A clone whose folder is gone, or that left, drops off the list when
  the record is read. A daemon counts as running when what answers at the port in its
  `daemon.json` proves it is that daemon (#371) and the clone's lock is held; no token is sent to
  find out.
- **The token doesn't travel with the tab.** The other page is another origin and needs its own
  token, which mustn't go in an address. So the page asks its daemon to open the clone. That daemon
  starts the other's if it isn't running (as `mur ui` would, replacing one of an older protocol) and
  asks it, on its sessions' port with the sessions' token from its `daemon.json`, for a key
  (`OpPageKey`, protocol 10): random, good once, for a minute. The tab goes to the other page with the
  key in the fragment (`#key=`), and that page trades it at `/api/key` for its token, which the tab
  keeps for that origin alone. The theme (#354) goes in the fragment too: the browser keeps it per
  page, and the other page is another page. `/api/key` is the one call that takes no token; it takes only JSON,
  which another site can't send without a preflight that nothing grants. The key left in the tab's
  history is spent. The cost: a page's token now reaches every joined clone's page on the machine,
  since that page can ask for keys to them, where before it reached its own; it still never reaches
  a sessions' port. A draft in a composer has to be sent or cleared first, as it lives only in the
  page. #303 (Karthik's draft, a private file that opens the page rather than the address) fits:
  `mur ui` could put a key in that file in place of the token, so the file would hold nothing that
  lasts.
- **What it shows.** Every device the daemon knows, this one first, with each session's name,
  agent, branch, what it is doing, when it started and when it was last active (#21), and the
  branches it has committed to when there are three or fewer. Whether a device is reachable, and
  when it was last seen, come from presence going stale. The worktree's folder name is shown for this
  device's sessions only: presence doesn't carry it, and a path from another machine would mean
  nothing here. Messages are every one this daemon reads, whoever it is for (every record is
  encrypted for the whole team; see The bus is encrypted), oldest first as this daemon read them, which is not by the
  senders' clocks, until earlier ones are read back (next). The daemon keeps the last 500 in memory.
- **Messages from before the daemon started are read back from the bus** (#138, 2026-11-12). The
  daemon exits when idle, and the next one had read nothing, so the page opened on conversations
  with their beginnings gone. A daemon away for more than 10 minutes also passes over what was sent
  meanwhile, which left a hole in the middle of one. The issue left two ways open: keep the page's
  messages in a file beside `state.json`, or read them back. They are read back, from the logs this
  clone has already fetched: the newest commits across every device's log, up to where the daemon
  has read each, opened with the keys it holds (`internal/daemon/history.go`, `bus.Repo.Recent`).
  - *Why not a file.* A file holds what a daemon read, and a daemon never reads what it passes
    over, so the hole would have stayed. It would also be a second copy of every message, in the
    clear, to keep in step with the bus. A person's own messages stay in their file (see "A person
    on the page"), which goes back further than the page's 500.
  - *What it costs.* Four git commands, however many commits are read, up to 4,000: the sizes of
    all of them are asked at once, and they are read in one go while they fit in 8 MB. Each command
    took about 0.2 s on the Windows laptop (2026-11-12). No request goes to the remote. It runs in
    the background, and not before a page or a `lookup` asks: most daemons are started by a session
    and never show the page. After that it runs again once the first syncs have passed over a
    backlog, when a backlog given up on is fetched, and when the device is given a team key, which
    opens what it couldn't read. The page's first calls, and a `lookup`, wait for the first read,
    3 s at most, so the page opens with its conversations in it. One call waits that long, at most:
    if the read hasn't come by then, later calls don't wait. A read is given 30 s, and one that
    fails is tried again a minute later, three times in a row at most.
  - *What changes with it.* The page shows messages from before this device joined, which every
    device may read (The bus is encrypted). No session is given them: a sync still passes over
    history. Once anything is read back, all the page's messages are in the order of their
    senders' clocks, and which commits are the newest goes by their writers' clocks, so a device
    whose clock runs ahead takes more of the 500. `lookup` shows the same messages. A message
    that reaches the open conversation after the page has loaded, but was sent before it, isn't
    read out as new; an unread one to the person still is.
  - *What it doesn't do.* The page can't reach past the newest 500; reading further back when the
    page asks isn't built. A record this device can't open is left out until a key opens it. While
    a backlog given up on hasn't been fetched, the whole log of its device is off the page, since
    the cursor is at a commit that isn't here; it comes back when the fetch lands.
- **How it's laid out** (#179, 2026-11-08; drawn as a sibling of T3 Code 2026-11-09, so a person who
  works in T3 Code reads it without learning a second app): sessions down the left under their
  devices, as T3 Code's threads under their projects, then a column of conversations (direct
  messages and group chats in one list, newest first, narrowed to either with a switch), then the
  open conversation as T3 Code's timeline: a person's own messages in bubbles on the right, agents'
  as plain text under a line naming the sender, and the composer docked under it. On a phone the
  sidebar is a drawer, and the list and a conversation take turns. A conversation is two sessions'
  messages to each other, a device's, or everyone's: the bus has no threads. Each session carries a
  dot: green active in the last five minutes, yellow idle, red do not disturb (the
  `do-not-disturb` tag from #211, or wake-ups set to Never), grey when its device is unreachable.
  The only other color is the accent and a pull request's state; faces are plain monograms. A
  settled session (#189) is absent from presence but still in the messages, so the page folds it
  under its device and keeps its conversations, out of the way; a direct message with a settled
  session folds away too. Sessions in Claude Code, Codex, pi or opencode never settle: one left
  open in a forgotten terminal stays in presence for good.
  So a running session idle for a day, or one a person hid (the `hidden` tag, set from the page
  through #211's `/api/tags`), folds under its device as quiet. An idle one comes back once it
  does anything; a hidden one stays hidden until someone shows it again, because a tag a person
  sets stays until a person takes it off. An open session's tags show under its name, where a person can add one or take one off.
  Since #270 (2026-11-09) tags also organize the lists: each sidebar row and card shows its session's
  tags, the sidebar's Tags group filters every list of sessions to one tag, and a tag menu, from a
  row's tag button or from sessions picked together on All sessions, puts a tag on them or takes it
  off, and makes new ones. It sends one `/api/tags` change per session, so it is the same human
  tag as before, travelling the same way; until a session's device has applied it, the chip shows
  as waiting, for two minutes at most. Tags with a meaning of their own show as that meaning rather
  than as chips: `model:` beside the agent, `do-not-disturb` as the red dot, `hidden` as the quiet
  fold, `human` on a person. There is no grouping by tag: a session can carry several, so groups
  would repeat it, and a filter shows one group at a time. Since 2026-11-09 Settings has a theme, kept in the browser: Dark (T3 Code's), Dim (the
  soft greys the page had before it followed T3 Code), Light (T3 Code's light theme) and, since 2026-11-10, Murmur (the website's blue, butter and cream, with an ink sidebar; `docs/brand.md`). Each is a
  set of color tokens over one design, so a theme costs only its palette. Started times, worktrees and committed branches left the page with the old
  Sessions view; `lookup` still has the branches.
- **Recent is a feed of what agents did** (2026-11-12, #448, Tyler's request), not only of what
  they said. Between the messages it shows a session joining or leaving, its status or tags
  changing, its commits, and pull requests and issues opened, merged or closed. The issue left
  three questions open; what was chosen, and why:
  - *Where the rest comes from.* Nothing new goes on the bus. A session's start and stop are
    events every device already writes. A status or a tag changing is the difference between two
    presences of its device, this one's own included, which the daemon sees anyway. Pull requests
    and issues are asked of GitHub with `gh` when the page is viewed (`gh pr list` and
    `gh issue list`, the thirty updated most recently of each, kept two minutes), as the pull
    request a branch heads already is: only people read them, so they cost the bus nothing and an
    agent has nothing to post. Commits come from the hook's credit (#21) on the session's own
    device, with their count; from another device only the branch shows, as presence carries the
    branches a session committed to and not the commits. Reading pushes off fetched refs (#178
    wants the same) would give counts for every device, and wasn't built.
  - *What is noise.* A feed, not a log, so what repeats is one line. A session's status changes
    less than 30 minutes apart show the last one, with how many came before; its commits to one
    branch in that time are added up, with the last subject. A session's first status isn't a
    change. A tag's line names what was added and removed. Heartbeats, `lastActive` and the
    branch a session is on are left out.
  - *Filtering.* All, Messages or Activity, and one session's or everyone's. Messages alone is
    the page as it was. Neither choice is kept over a reload.
  - *Who did it.* Agents on one machine share their user's GitHub login, so a pull request is
    named for the session on its branch (or that committed to it), and for the login only when
    no session is. GitHub's list says who opened one, not who merged or closed it, so those
    lines name nobody but the session whose branch it was. An issue is the login's.
  - *What it doesn't have.* The daemon keeps the newest 600 in memory. A daemon that has just
    started reads the sessions' starts and stops back from the bus with the messages (#138), and
    GitHub remembers pull requests and issues. A status, a tag or a commit from before it started
    is gone: those were never on the bus, and only a file on this device could keep them, which
    isn't built. A change seen only as a difference between presences is
    one per presence write: two status lines set within a write show as one. A repository with
    no `gh`, or one that isn't logged in, shows no pull requests or issues, and says nothing.
- **Settings** (when a session is woken, and what a busy one is given by the hook) are saved in
  `.git/murmur/settings.json`, replaced whole; "Delivering a message as a turn" says what they do.
  Settings also lists every mute (see "Mutes"), with a button to undo those of this device's sessions.
- **Open page, running daemon.** The page asks for its state every 3 s, 15 s in a hidden tab. A
  call that carries the token, from a page someone can see, counts as an open page for 2 minutes,
  and an open page stops the 10-minute countdown the way a session does: a daemon with no sessions
  and no T3 thread to wake exits about 12 minutes after its page is closed or hidden (a background
  tab, a minimized window, a locked screen). A hidden page marks its calls, so a tab forgotten in
  the background doesn't keep the daemon, and its polling of GitHub, up for good (Opus review,
  2026-10-29). A request without the token doesn't count.
- **This device's health** (`GET /api/health`, 2026-11-10, data only: where the page shows it is
  Tyler's call). Peers are judged stale against this daemon's last successful sync, so a device
  that has lost the remote shows every peer as it was when the sync stopped, and nothing said so:
  #141's refused pushes and 52-minute DNS outage, and #87, were found by reading `daemon.log` by
  hand. The snapshot says how the syncs, pushes, fetches and handoffs have gone since the daemon
  started, and what became of the public repo's report, the one thing Murmur sends; its fields are
  `web.Health` (`internal/web/health.go`). An error is a class and one line, and a line that may
  hold a credential is not shown. It is read from memory and one small file, behind the page's
  token like the rest of the API, and goes nowhere else.
