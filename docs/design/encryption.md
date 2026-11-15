# The bus is encrypted

Tyler's decision (2026-11-08, #156): what goes over the repo is encrypted, in every repo, public or
private, with **no friction for users or agents**: nobody runs a command, approves anything, copies
a key or makes a choice. The daemon does all of it. An earlier design (#160, closed) made it a
setting someone turned on with `mur encrypt`, and that command was the friction.

Tyler's decision (2026-11-10): **every device that can push, and has published its key, reads
everything ever sent on the bus**, direct messages included. A device that was away reads what it
missed, and one that joins later reads the history. #257's first design encrypted each record for
the devices seen in the last 6 minutes, so a laptop closed overnight came back to a gap it could
never fill, and a new device could read nothing from before it joined. That design is replaced by
a team key, below.

- **Device keys.** At start, each daemon makes any key it lacks: an Ed25519 signing key and an
  X25519 box key, in `.git/murmur/keys` (0600, beside the device ID; no commit or checkout touches
  it). It publishes the box key in its presence and seals the presence with the signing key. The
  seal is an Ed25519 signature over the record's JSON, bound to the ref (`internal/bus/seal.go`,
  from #159).
- **Push access is the key directory.** Only what can write `refs/murmur/*` can publish a key and
  be granted the team key (below); GitHub's own access control decides who is on the team. **That
  is every way to push, not only collaborators** (#397, 2026-11-10): a deploy key with write access,
  an org team or the org's default permission, a GitHub App that can write contents, and a workflow
  that runs untrusted code with a write token (a `pull_request_target` workflow that checks out and
  runs a pull request's code). Git records what a ref points at, not who pushed it, so a daemon
  can't tell a collaborator's push from a workflow's. An outsider who gets one push through such a
  workflow publishes a device of their own, is granted every team key, and reads the bus, history
  included; a rotation changes nothing while the workflow is there. Such a workflow can already
  push code. Opening a pull request can't do it: that writes only `refs/pull/*`. Checking who
  pushed, through GitHub's activity API, is open in #397.
- **Pinning.** A reader pins a device's signing key, in `state.json`, the first time it reads a
  sealed presence from that device. From then on it takes a presence as that device's only if it is
  sealed with that key: none unsealed, none with another (`internal/daemon/keys.go`). A presence
  that fails these checks is refused and logged, the pin stays, and nobody is asked anything. A real
  device never changes keys under its ID (see key loss), so a mismatch means a forgery or a copied
  `.git`. Pinning can't stop someone with push access who seals a presence for a device before a new
  reader has seen the device's own. That reader then refuses the real device. The attack takes push
  access, and the trust model already trusts push access with code. Each presence is signed with a
  time that never goes back on its writer, and a reader takes one as new only if it was signed later
  than every other it has taken from that device (a high-water mark in `state.json`), so a presence
  put back on its ref, or wrapped in a new commit, is never new (Sol's reviews of #257). A device
  whose binary is rolled back to one from before encryption writes unsealed presence, which its
  teammates refuse once they have pinned it, until the updater brings it forward again.
- **The team key.** One shared ref, `refs/murmur/team`, holds a chain of sealed records, pushed with
  a lease as the name registry is, so two devices can't both make the first key
  (`internal/bus/team.go`, `internal/daemon/team.go`).
  - An *epoch* publishes a team X25519 public key in the clear, with its private half wrapped for
    its writer's box key. The first daemon to find the ref empty makes the first epoch.
  - A *grant* wraps every team private key its writer holds for one device. Each device puts, inside
    its encrypted presence, the ID of the newest epoch it has read and the IDs of the team keys it
    holds. A daemon that holds the current key grants to each device with a pinned box key whose
    sealed presence names the current epoch and lists fewer keys than the daemon could give it
    (Tyler, 2026-11-10).
    - **Naming the epoch is the proof of push access.** An epoch's ID is the hash of a random key, so
      a presence that names it was signed after the epoch was made, and it sits on the device's own
      ref, so it was pushed after too. A collaborator removed before a rotation can read the new ID
      on a public repo and sign a presence naming it, but can't push it. An older presence of theirs
      put back on the ref names an older epoch. No time is trusted.
    - **Nobody has to be online together.** The presence's age doesn't matter, nor whether anyone
      saw it arrive. A device that joins while nobody is around pushes its presence and can go; the
      first key holder to sync grants it, and it reads the grant when it next runs. An earlier rule
      granted only on a presence a daemon watched arrive, and such a device then got nothing.
    - **By what a device holds, not by what the ref shows.** A grant that doesn't open, or carries a
      key under another's ID, leaves its device without the key while the ref shows it granted. The
      list in the presence shows it still lacks one, so it is granted again.
    - **A device that is away isn't granted over and over.** Its presence stays as it left it. A
      daemon that has read a grant for a device grants it again only on a presence signed later
      than the newest it held when it read that grant (`GrantMark` in `state.json`). Both times are
      that device's own. So a device with a junk grant gets a real one after its next presence, not
      before; and one presence pushed between a grant and reading it can earn a second grant.
  - A *roster* lists the people who can push to the repo, encrypted for one epoch's team key, so
    every device granted that key reads it. `mur rotate` writes one and compares against the last.
  - A key under a team key ID that isn't its own is dropped when a grant is opened, so a grant can't
    hand a device a key under another's name.
  - Team private keys a device is granted are kept in `.git/murmur/team-keys` (0600), which only
    grows: a rewritten team ref takes nothing a device could read away from it. If that file is lost
    while `state.json` stays, the daemon reads the team ref again from its start at its next run: its
    grants are still there, so the keys come back with no teammate around.
- **Records.** Each event and presence is encrypted once, with a fresh content key wrapped the way
  age does it, with an ephemeral X25519 key, HKDF-SHA256 and AES-256-GCM, all from Go's standard
  library (`internal/bus/box.go`). A presence is encrypted for the current epoch's team key alone.
  An event is encrypted for that key and for its writer's device: a daemon reads its own log to
  queue a message from one of its sessions to another, and would otherwise stop at its own record
  until it was granted the key. The team key's
  public half is readable by anyone, so a device can write before it holds any private half. An
  event is written as `{"v":1,"type":"box","box":{…}}` with the whole event inside: sender,
  recipient, type, subject, body, tags. A presence keeps its device ID, time and box key readable and
  encrypts its host, owner, sessions, offline sessions, and the epoch and team keys a grant goes by. Before a device has read any epoch, it
  encrypts its presence for itself alone, and pushes it again when it reads one. Commit subjects are
  `event` and `presence`; they used to repeat the content.
- **A device waiting for a grant** can send at once. Reading a log stops at the first record
  encrypted for an epoch it hasn't been granted, and goes on from there once it is, so nothing is
  missed. A box for a team key the team ref doesn't show waits as well, until a sync that began
  after the record was first met has read the team ref whole: a rotation's first records can be
  fetched before its epoch is read, when the team ref's fetch fails or a log's fetch goes past what
  the sync listed, and passing over them would lose them for good. If that sync shows no such key
  the box is junk and passed over, so junk holds a log up for one sync. The stretch of a log whose
  fetch was once given up on, read for its tags alone, stops and goes on the same way. While it
  waits, teammates' presence shows with no sessions. **The grant comes when a key holder next
  syncs**, which is the floor for any scheme where only devices hold keys. Messages read late this way can reach a session that
  started within the minute after they were sent, by the allowance `queueLocked` gives a sender's
  clock (found in `TestRestartCatchesUpReconnectingSessions`, 2026-11-10).
- **Rotation.** A new epoch is a new team key, and everything is encrypted for it from then on. It
  is wrapped for its writer alone; every other device gets it by a grant, which takes a presence
  that names it. A collaborator removed from the repo can't push, so never gets it, **unless
  something else of theirs still can**: a deploy key they added, access through an org team, an App
  (#397). Removing someone means removing each of those, or a rotation keeps nothing from them. A device that
  joins later is still granted every old key, so history stays readable. Only a device that holds
  every team key may rotate, since the devices it grants get the keys it holds; that is checked
  again on each try, as another device may have rotated meanwhile.
  `mur rotate` lists the people who can push to the repo with `gh` and makes a new epoch if anyone
  on the current epoch's roster is no longer among them, or if this device can read no roster for
  it; `--force` always does. Someone moved down to read access counts as gone. When nobody has left
  and someone has joined, it writes the roster again with them on it, and makes no new key; if a
  teammate's device wrote a roster meanwhile, it compares again with that one first, since writing
  over it could take off someone who has left with no new key made.
  **Someone who joined and left between two runs was never on a roster, so their leaving isn't
  seen**; only `--force` covers that until rotation runs on its own. When it should is open (Tyler,
  2026-11-10: the structure first).
- **Fails closed.** A daemon with this change never writes content in the clear. A message to a
  device with no key is refused, and the sender is told that device runs an older Murmur. A write
  made before any epoch exists waits for the first, which the first sync makes.
- **Older daemons.** A daemon from before this passes over `box` events, as it does any type it
  doesn't know, and sees an updated device with no sessions. An updated daemon reads the older one's
  plaintext as it always did. It reads a plaintext event from any device, since events carry no
  signature to hold them to, and plaintext presence only from a device it hasn't pinned. So for a
  while older devices can't hear updated ones, and messages to them are refused. The updater (see
  Updating) closes that gap within an hour of the release, with nobody acting. A version from before
  the updater needs the manual install it already needed. **Until it updates, an older daemon writes
  in the clear, and nothing can stop it.**
- **A collaborator removed from the repo** keeps every team key they were granted. On a public repo
  they can still fetch the bus, so **they read everything written until someone rotates**, and
  everything before. On a private repo, removal takes their read access with it. #257's first design
  dropped them within 6 minutes with nothing to run; the team key trades that for history and for
  devices that come back.
- **No forward secrecy.** A leaked device's keys read everything up to the last epoch it was granted.
- **Key loss.** A deleted clone comes back as a new device, and is granted the team keys like any.
  If only the keys file is gone and `state.json` shows the device had keys, the daemon takes a new
  device ID, because its teammates would refuse a new key under the old one. The old ID's presence
  goes stale, and its names stay taken. With `state.json` gone too, the clone's local copy of its own
  presence ref still shows a seal, which tells the daemon it had keys. Only a daemon meeting
  encryption for the first time has neither, and keeps its ID.
- **The name registry stays in the clear, and unsealed.** It holds random names, device IDs and
  random session IDs. A device must read all of it before it can name a session, which may be before
  it is granted the team key. For the same reason an encrypted presence carries its sessions' names
  and IDs beside the box: a device that can't open it yet still knows those names are taken. That
  exposes nothing the registry doesn't.
- **What anyone who can read a public repo still sees:** device IDs, which begin with a slug of
  the hostname; when each device writes, how many records and how large they are; the team key IDs
  each record is encrypted for; which devices each team key was granted to, and when; each device's
  public keys; the name registry; and `refs/murmur/reported`. They don't see messages, subjects, who
  sent what to whom, statuses, branches, tags, hosts, owners, agents, or who is on a roster.
- **Open, for Tyler** (#156): whether the census (#199) should keep anything readable, such as
  `owner` or an event's kind, now that encryption hides them from it (since #550 it estimates
  messages and sessions instead); whether new clones should
  get a device ID without the hostname in it (#330); and when a rotation should happen without
  anyone running `mur rotate`.
