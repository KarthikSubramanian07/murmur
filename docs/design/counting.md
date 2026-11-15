# Counting use

Tyler's goal for the project is 100 active users (2026-11-02), and the transport hides them: every
team's traffic stays in its own repo, and release downloads count installs, not use.

Tyler's rule (2026-11-08, #199): **the only thing Murmur sends us is a public repo's address.** A
public repo's bus is readable by anyone, so once we know the address, its repos, devices and
records can be counted from the bus itself (since #156 what the records say is encrypted, which
hides messages and people from such a count), and the address is already public, so there is no
opt-out. A private repo sends nothing and isn't counted. Weighed and turned down: a GitHub topic on
each repo as the marker (it changes how the user's repo looks), and an ID for private repos (a
hash of the address can't be salted for real, since the salt would ship in the public binary, and
a random ID is still data from a private repo).

`internal/publicrepo` does it, from `mur init`, `mur join` and daemon start:

1. If `refs/murmur/reported` is on the remote, stop: some clone has reported the repo. A daemon's
   sync doesn't fetch it (it reads only mesh, presence and names), so the remote is asked with one
   `ls-remote`, and once the ref is found or pushed, `publicrepo.json` in the state dir keeps the
   address it belongs to, so this clone doesn't ask again unless its remote changes.
2. Reduce the bus remote's URL to `github.com/<owner>/<name>`; anything else is not reported.
3. Ask GitHub `GET api.github.com/repos/<owner>/<name>` with no credentials. A private repo answers
   404 to that, so no token is involved. Private, or not on GitHub: look again the next UTC day
   (`publicrepo.json` again), so a repo made public later is reported within a day.
4. POST `{"repo": "github.com/<owner>/<name>"}` to `https://murmur.afig.dev/v1/repo`, spelled as
   GitHub spells it, with no version in the User-Agent. The site stores it only once GitHub
   confirms the repo public (when GitHub can't say, it answers 503 and the daemon sends it again
   later): one row per repo, its address and first day, no IP addresses (`site/src/usage.js`,
   `site/migrations`). A repo already stored is answered 200 from that row, without asking GitHub
   again, and a body not sent as `application/json` is refused with 415, so a web page can't have
   its visitors' browsers send these POSTs without a CORS preflight, which the site doesn't answer
   (#299). Rate limits per IP, and which token the site asks GitHub with, are set in Cloudflare,
   not here.
5. Once the site has answered 200, note in `publicrepo.json` that this address was sent, then push
   `refs/murmur/reported`, a root commit whose body is `{"v":1,"at":...}`, leased so it is created
   only if absent. A push that fails is retried at every later look, without sending the address
   again, and a daemon keeps looking until the ref is there. Two clones racing both send; the site
   keeps one row. Deleting the ref makes the repo report again from a clone that hasn't seen it.

So each public repo reaches us once in its life. A development build (`-dev`) never reports.
Daemons older than this ignore the new ref: their sync reads only mesh, presence and names.

Earlier versions (#148) sent a daily check-in to `/v1/checkin` (a random user ID, a hash of the
remote, the version, OS and architecture). Since 2026-11-08 the site stores none, but still answers
with the latest release; the rows already stored are kept as history at `/stats/archive`.
`mur version` now hears of newer releases from the updater, which keeps the latest release it saw
in the state dir's `latest.json`; with updates off it hears of none.

The counts come from the census (#259), which reads what each reported repo shows anyone. Once a
day a GitHub Action in this repo (`.github/workflows/census.yml`, `site/census/census.mjs`) fetches
the commits, without their files, of each repo's device logs, presences and name registry
(`refs/murmur/mesh/*`, `refs/murmur/presence/*`, `refs/murmur/names`) and of its default branch, and sends the
site counts and hashes, never content: per UTC day, the sessions started (each once, though a
daemon that restarts announces its sessions again), the messages, all records and the bytes their
events come to, and a hash of
each device that wrote anything, presence included; a hash of each device's owner, the git user name
its presence shows; and for each contributor, a hash of their GitHub login or email and the day of
their first commit, so one person in two repos counts once. The site only ever raises a count and
keeps every device and owner it has seen, since a bus log can be rewritten and a daemon deletes its
presence when it stops. It runs from GitHub,
not from the Worker, because counting needs git, and a fetch of the whole bus is one request where
the API would need one per commit.

Since 0.3 every event is encrypted (#156), with its kind sealed inside, so the census counts an
encrypted log's records exactly but can't read which are messages or session starts. Tyler's
decision (2026-11-13, #550): it estimates them, since 0 is wrong and "unknown" says too little, and
the stats say which figures hold an estimate: the page with an asterisk on sessions and on
messages, the report with ≈ on each such figure. The census decrypts nothing and holds
no key. It reads only public repos, and uses two things any reader of one sees (see "What anyone who
can read a public repo still sees"): how large each record is, and the name registry. A size gives
away roughly what kind of event a record is, never what it says.

- **Messages, from size.** An encrypted record's length gives the length of the event inside it
  exactly, and a message is longer than a session's start or stop. An encrypted event 180 bytes or
  more longer than its device's ID counts as a message. Measured on 2026-11-13 against the
  unencrypted history of two public repos (155 messages, 1,094 session starts and stops), that cut
  missed 8 messages, 5%, each with no body and a short subject, and took no session event for a
  message. So the estimate runs about 5% low on traffic like that. It can run high where that
  history had nothing to measure: a start reads as a message once its agent and branch together
  pass about 55 characters (the longest seen were 47), and so does a tags or group-add event with
  some 40 characters of tags. It had no message to a group or to everyone either, which is
  shorter and so missed more often. Checked the same day against the truth on four buses Tyler's
  machine could read whole (three with their team key, opened only for each event's type and
  lengths; 868 messages and 1,598 other records, most of them not among those measured), the cut
  missed 19 messages, 2.2%, and 0 to 5.2% by repo, and took no other record for a message. The
  longest start came within 7 bytes of it.
- **Sessions, from the name registry.** Size can't count them: a start announced again after a
  daemon restarts, a tags event and a group-add are all the size of a start, and start-sized
  records came to 1.3 to 3 times the sessions on the buses measured. The registry isn't encrypted
  and records each session that takes a name, by its ID: it held 445 of the 446 sessions
  that history started. A session it names that no unencrypted start shows counts as estimated,
  on the day of its registry record. In that check it gave, with the exact count, 569 of the four
  buses' 570 sessions. A session another session started takes a name but, since #487, writes no
  start, so the registry counts it too.

The estimates are sent and kept apart from the exact counts (`est_sessions`, `est_messages`), and
`site/census/census.mjs` has the measurements. The site only ever raises them, as it does every
count, so if the census later estimates differently and lower, what is stored stays until someone
lowers it in D1 by hand. An updated device's presence hides its owner too, so
the census can't tell its person apart; whether that should stay readable is open on #156.

What encryption doesn't hide is how many records a device wrote and how large they are, so the
stats page is built around those (Tyler, 2026-11-13). Bytes are the size of each record's event as
JSON, which for an encrypted event is the event inside the box, so a log weighs the same encrypted
or not. They grow with what agents say to each other, which makes them the exact measure of a bus's
traffic. The page shows repos and users first, then what is measured (records, bytes, devices,
contributors), then sessions and messages, the figures that matter most, estimated where a bus is
encrypted.

Each repo is counted on its own, since anyone can list one (#298). A repo that can't be read (gone
private, or deleted) keeps the counts it had. A repo may take 3 minutes, for its git and its
account lookups, and show 2,000 refs of devices, so that one with huge objects, 100,000 refs or a
slow API can't hold the job past its 30 minutes. A repo over either, or too big for the site's
limits on what it keeps, is skipped. The run warns of it but stays green: a repo skipped today is
skipped every day until someone takes it out of D1, and a run red every day teaches people to
ignore red. Counts the site refuses do fail the run, after every other repo has been sent. The
census sends only what the site's limits allow, so a refusal means the two disagree, or the site
is down, which can be fixed.

A green run can still leave repos uncounted, so the stats say so, from `repo_census.counted`, which
the site already keeps: `/stats/report.md` ends with when the census last ran, and, once that is
over 30 hours ago, that it hasn't run for that long; then the repos that run didn't count, each
with when it last was. A repo listed since that run's day and not yet counted is new, not missed,
since anyone can list one and most days someone does (`/stats/data` has the same, and the page's
footer leads with it only when something is behind). Each run's page in Actions has a table of
every repo, a row written as each is done, so a run cut off at its timeout keeps what it did.

A time on the bus or a branch is its writer's clock, and can say any year up to 9999. A day after
tomorrow (UTC) is never believed, since one would keep a repo active and its people new for ever,
and nor is a bus day before 2026-10-01, when there was no bus. A record with such a time is dated by
its commit, or isn't counted. A commit dated in the future counts its author as of the day it is
read. The site refuses such days, and the stats count nothing after today.

`/stats` shows those repos and the counts over time; it needs a key: Tyler's, or one he gives an
agent, which reads it as markdown at `/stats/report.md` or JSON at `/stats/data` (#150).
