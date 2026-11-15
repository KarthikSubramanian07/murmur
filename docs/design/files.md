# Files with a message

#570, 2026-11-15. A user's feedback (2026-11-14): "Not being able to send images in the chat is
also a bad thing tbh... limits the potential of the agent." Until then Murmur carried text: an agent
couldn't send another a screenshot, a log or a diff.

`send` takes `files`, a list of paths. The recipient's agent gets each as a path on its own machine,
which it opens with its own tools; one that reads images sees the image. The page doesn't show or
send files yet (#570's second part).

## How a file travels

- **A ref for each message.** The sender's daemon encrypts each file and writes the blobs as one
  commit on `refs/murmur/files/<device>/<message id>`, whose tree names them by their place in the
  message (`0`, `1`, ...). It is plumbing, like every bus write (`hash-object`, `mktree`,
  `commit-tree`, `push`), and pushed before the event, so a daemon that reads the event finds the
  files there.
- **Not in the log.** A device's log is fetched whole by every device, for good. A file in an
  event's commit would be downloaded by everyone and never leave. On its own ref, a file is
  fetched only by the devices its message is for, and goes when the ref does.
- **Syncs don't fetch them.** A sync fetches the refs it reads (`readable` in
  `internal/daemon/sync.go`) and leaves the rest of `refs/murmur/` on the remote, which daemons
  from before files do too. The cost: the listing every poll grows by a line per files ref.
- **Encrypted with a key in the event.** Each file has a fresh AES-256-GCM key; the event, which is
  encrypted for the team ([encryption](encryption.md)), carries each file's name, size, SHA-256
  and key. So whoever can read a message can read its files, and nobody else. The blob is bound to
  its message and place, and a receiver holds what it opens to the event's size and SHA-256. Anyone
  who can read the repo sees that a message had files, how many, and about how large.
- **Fetched before the message is queued.** A daemon that reads a message with files for one of
  its sessions fetches the ref, saves the files under `.git/murmur/files/<message id>/`, never the
  working tree, and only then queues the message, so a path in a turn works when the agent reads
  it. The fetch stores no local ref. It gets three tries of a minute each; then the message is
  queued without the files, and the turn says which aren't there. A ref that isn't on the remote,
  or holds something else, costs one try. The wait survives a restart (`state.json`).
- **On the sender's device** the daemon keeps a copy where a receiver's would be, so a session on
  the same device has the files with no fetch.

## Limits

Tyler hasn't ruled on the numbers (2026-11-15); they are `MaxFiles`, `MaxFile` and `MaxFilesTotal`
in `internal/bus/files.go`, and `FileKeep` in the daemon's options.

- **8 files a message, 10 MB a file, 20 MB a message.** Every recipient's device downloads them,
  and a push has a minute.
- **A week on the remote.** The sender's daemon deletes a files ref 7 days after it sent it, and
  the remote's own housekeeping then drops the objects. Receivers delete their copies after 7 days
  too. The message stays in history with its file names; a turn made from it later says the files
  aren't there. A sender that never runs again leaves its refs.
- **A send to several recipients uploads the files once for each**, since each gets a message of
  its own (#520).
- **In the clone**, the blobs a daemon wrote or fetched stay in `.git/objects` with nothing
  naming them until git's own garbage collection, two weeks by default.

## What is checked

- **Names.** A file's name becomes a name on the receiver's disk, so a reader cuts it to letters,
  digits, `.`, `_` and `-`, 60 characters, never a path, a hidden file or a Windows device name,
  and makes the names in one message distinct (`bus.FileName`, `bus.CleanFiles`). The sender runs
  the same, so both sides agree on the names.
- **Secrets.** A file that is text goes through the check a body does (`internal/scrub`). An image
  or other binary can't be checked.
- **Sizes.** A sender is refused past the limits. A receiver reads a blob's size before the blob
  and doesn't read one over the limit: git stores a blob compressed, so a small fetch can hold a
  large one.
- **Not checked: what a fetch brings.** A files ref holds whatever its pusher put there, and a
  listing gives no sizes, so a fetch can't be capped before it runs; a `--filter` would make the
  user's clone a partial clone for all its later fetches. Push access is already trusted with code.

## Older daemons

A daemon from before files ignores an event's `files` and gives its sessions the text, with no sign
there was more. A device's presence says whether its daemon takes files (`files`, beside `groups`
and `receipts`), and a sender is told which recipients' devices don't. The message is sent all the
same: refusing it would hold back the text for a gap the updater closes within an hour
([updating](updating.md)).

A client from before files talking to a newer daemon sends none. A newer client finds an older
daemon by its protocol number (22) and replaces it, as for any request an older daemon can't serve.
