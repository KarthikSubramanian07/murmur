# One daemon per device, a thin MCP server per session

```
GitHub (refs/murmur/*)
        ▲  one poller, one writer per device
  mur daemon  (per device, per repo)
        ▲  local socket
   ┌────┼──────────┬──────────┐
 MCP   MCP        MCP       page
(T3)  (Claude    (Codex)   (humans)
       Code)
```

One device runs many sessions, often across worktrees. The daemon is the only process that
writes the device's ref and the only one polling GitHub; sessions connect to it. Each session
starts its own MCP server process (that is how Claude Code runs stdio MCP servers), and the first
to start launches the daemon. Under a job that forbids breakaway, as Codex's MCP servers are on
Windows, the daemon is started through WMI so it outlives that session (2026-10-29). A session's
MCP process can restart while its conversation carries
on: T3 Code restarts it on every interrupt, after 30 minutes idle and on every app restart (one
thread's restarted 8 times in about 2 days, 2026-10-28), and Claude Code on `/mcp` reconnect and
on resume. So a dropped connection starts a 60-second grace (2026-10-28): the session stays in
presence, and a hello with the same session ID in that time takes it over with its inbox and
unread notices, writing nothing to the bus. Only when the grace runs out does the daemon announce
`session.stopped`, even after a crash. A stopped session's inbox is kept 24 hours, across daemon
restarts, and given back if the session returns. It holds only what its daemon received: a daemon
exits 10 minutes after its last session or open page, unless a T3 thread it could still wake is
waiting ("A thread that is idle", below), and one back after longer than that skips what it
missed, except for the sessions whose inbox is kept (2026-10-30, #30): for a session stopped
within the 24 hours it queues what was sent after the session stopped (by the sender's clock,
with a minute's allowance), which includes a message to the whole device or to everyone, as the
live ones do and as the shorter outage's catch-up does. Before then a laptop shut overnight lost
every message sent to its stopped sessions. A first start has no stopped sessions and skips all of
it. A message is addressed on the bus to
`device/session`, which a session's name stands for (see Session names); presence carries enough
description (name, agent, branch, what it is doing) for an agent to choose a recipient. Nothing sent before a session first started is replayed to it. A
daemon back after less than 10 minutes down queues what it missed for the sessions that
reconnect within 30 seconds of its start, and for sessions stopped within the 24 hours.

Sessions reach the daemon over loopback TCP, on a random port written with a random token to
`.git/murmur/daemon.json`. A client trusts that file only while the clone's lock (`daemon.lock`) is
held (2026-11-09). A daemon that dies without removing the file (a crash, SIGKILL, a power cut)
leaves an address another user's process could listen on, to be sent the sessions' hellos and
requests and to answer with messages of its own. A daemon takes the lock before it writes the
file, removes the file when it begins to stop, before it gives up its port, and lets the lock go
only when it has gone. So a daemon.json beside a free lock is stale and is removed, a starting
daemon removes one as soon as it holds the lock, and a client that loses its daemon forgets the
address. Clients take the lock only shared, and only a daemon takes it exclusively, so a lock a
client finds held is a daemon's; a timed retry left a window where another client's cleanup looked
like a daemon. A client from before this takes it exclusively for a moment while stopping or
replacing a daemon. This holds where the state directory is private to its user: it is made 0700 on Unix, and on Windows
gets an access list of its own (below).

The lock is checked when a client finds its daemon, and a client goes on using the address after
that. A daemon that died later left its port to whoever took it, and that listener got the next
request, the token and any outgoing message with it, and could answer with an inbox of its own
(#371, from Sol's review before v0.3.0). So since protocol 12 (2026-11-10) every connection a client
opens starts with a ping that carries a fresh nonce and no token, and the daemon answers with an
HMAC of its address and the nonce keyed by its token. Only then does the request go, on the same
connection, so nothing can take the port between the proof and the request; a separate ping first
would have left that gap. What answers without the proof is not the daemon, and the client finds
its daemon again (or starts one). Until it is proved, a reply is read only up to 4 KB, and a proved
daemon's up to 64 MB, about half again a full inbox of the longest messages escaped: before, one
that never ended was read until the deadline, however much it came to. A daemon of protocol 11 or
older can't prove itself, and refuses the nonce's ping as a missing token. A client sends it
nothing at all, not even a stop, and so can't ask it to stop or to make way: it says the daemon is
from an older version, and how to end it, by the process ID in daemon.json, or that it exits on its
own 10 minutes after its last session, when the next command starts a current one (2026-11-10).
That is the cost of an upgrade from 0.2, which never updates itself, so everyone coming from it
installs by hand: a daemon left running by the old mur is in the way until it goes. `mur stop` and
`mur join` (and a leave, and `mur uninstall`) end it by its process instead, which sends it nothing
(#394, 2026-11-11): the process daemon.json names, once the clone's lock is held, the process is
alive, and the program it runs is mur, so that a process ID since given to something else is never
ended. On Unix it gets SIGTERM and shuts down as it does when asked; on Windows it is terminated,
with no shutdown, as a crash ends it. A session's own client still ends nothing: an agent's
session doesn't end a process on its own, and the person upgrading runs one of those commands.
Everything that talks to it was tried first and failed review (Sol's reviews of main before
v0.3.0-rc.1 and of #389). Talking to it unproved while the clone's lock was held left a gap between the check
and the dial. Checking after the dial still trusted a daemon from before 07a1ecba, which closes its
port as it begins to stop and keeps its lock and daemon.json while it finishes, so another process
can take the port in that time. Even a stop alone gave that process the token, which is also the
key the proof uses: it could then prove itself to the next client, beside the old daemon's lock and
file, and be sent its messages and answer with text the hook puts in the agent's context. A daemon
of 12 or later that is older than its client proves itself, and is replaced as before. Discovery still contacts the
listener before trying the lock, since trying the lock can turn away a daemon starting just then;
what the listener is sent there is the nonce alone.

A daemon answers, and writes daemon.json, before its first sync, not after it (2026-11-10, #372).
That sync lists the remote, fetches and reads what changed, and may take its whole 10 s; with the
process's own start on Windows, a first start of a new binary on Tyler's laptop took 21 s, and the
15 s a starter waits for an answer (`mur ui`, an MCP server) ran out on a daemon that was fine.
What the sync reads still comes first where it counts: a registration waits for it, at most those
10 s, so what it finds is the backlog and not a new session's, and so do who, send and lookup,
which on a new clone know no other device until then. The background loops start after it, as
before. A stop or a leave during it ends it.

A daemon with no sessions, no open page (see The page for humans) and no
T3 thread to wake for 10 minutes exits. The token lets its holder name the program the daemon runs
to hand a message to T3, so reading the file is running code as the user. `.git/murmur` is made
0700, and on Windows, where modes do nothing, each daemon start gives it an access list of its own
that lets in only the user, SYSTEM and Administrators, and rewrites the files already in it
(2026-11-09). Before that a clone outside the user's profile left it readable by every user. A
network drive, and a volume with no access lists, is left as it is: a share checks access as the
account its server knows, and a list naming the local user could shut them out for good. So is a
directory an elevated run made, which a later run without elevation may not change; that is only
logged. Any daemon that stops deletes its presence, so other devices drop it at their
next sync rather than when it goes stale.

A manual upgrade replaces the binary and not the daemon already started from it, which would go on
answering ping and refuse whatever came after the version it was built from (the route request
and handing a message over as a turn, #57, were the first). So the daemon reports a protocol number
(`proto.Protocol`, 1 as of 2026-10-30; a daemon from before the number reports none, read as 0) in
its ping reply, and a client that finds a lower number than its own stops that daemon and starts its
own: `mur join`, `mur ui` and an MCP server registering a session do, since each may start a
daemon. Only an older daemon is replaced, never a newer one, so two binaries of different ages on
one machine don't stop each other's daemons for ever, and a number is used, not the version string,
so that builds from a branch (which all say "dev") are told apart. A client that may not start a
daemon (the hook and the other commands, which never start one; `mur stop`, which ends an old one by
hand; a user who has left) takes the daemon as it is. The old daemon is asked to stop and
takes its time: it lets its background work finish for up to 20 seconds and then deletes its
presence for up to 15, holding the clone's lock throughout, and the new one can't start until the
lock is free. So the client waits for the lock for as long as a shutdown may take, 35 seconds, and 5
more (`proto.ShutdownMax`; it waited 15 s until #83), or less if its caller gives up first, and
fails with an error that names the old daemon's pid if the lock is still held. A session whose
daemon refuses its route (an older one that couldn't be replaced) works, with its messages as
notices, and its MCP server logs that once, to its standard error, so that whoever wonders finds
why. Raise the number with any change to a request or a reply that an older daemon can't serve.

The daemon's log, `.git/murmur/daemon.log`, is its only record of what it did, so it is what a
teammate is asked to send when their device misbehaves, maybe to someone off the team. So it can be
shared: it names events by type, ID and address, never by what a message says, and each line the
daemon's loggers write passes through `internal/logfile`, which takes the user and password, the
query and the fragment out of any URL, and cuts a line at 64 KB, with a note of how much it cut (a
starter's own lines and a panic's trace go to the file directly). It does name the clone's paths.
Past 4 MB it moves to `daemon.log.old` when a daemon starts, and while it runs it is copied there and
emptied, since Windows can't rename an open file. If the copy fails it is emptied anyway, with a
line saying so; if emptying fails, the lines that don't fit are dropped, and it is tried again a
minute later. A daemon whose log was moved under it, as a later start does past 4 MB, stops moving it and writes on to the moved file. Until
2026-11-09 it moved only at a start, so a daemon kept up for days by an open page grew it without
limit.
