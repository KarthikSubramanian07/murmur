# Tags, groups and lists

## Session tags

#198, first part built 2026-11-08: tags set by hand, the model, and search by tags. Groups followed
(#184, 2026-11-11; see Groups, below). Rules in a tag file and subscriptions are put off, perhaps for
good (#441), and the lifecycle state was dropped (#185). The first
version also had an availability state (#186): `unavailable` made `send` refuse a session and
broadcasts skip it. Tyler had it taken out before merging (2026-11-08), so no tag carries behaviour:
`unavailable` is a label like any other. Tyler then asked for `do-not-disturb` (2026-11-08), the one
tag with behaviour, below.

- **What a tag is.** Up to 40 lowercase letters, digits and `. _ : / -`, at most 12 on a session
  (`internal/tags`). A tag under `model:` names the session's model, and a session has one at most.
- **`do-not-disturb`** keeps a session's messages queued in its inbox. None is handed over as a
  turn (the daemon treats the session as one it may not wake). The hook gives neither a notice
  nor the whole message (#204), holds none for the turn's end (#202), and the `Stop` hook doesn't
  keep the turn going with them (#213), even while the session is at work. `inbox` gives them as
  usual. When the tag comes off, each goes out once, by whichever route applies then: handed over
  as a turn if the session is idle, or, if it is at work, a notice or the whole message from the
  hook, and given at its turn's end. A message queued through the tag counts as arriving when the
  tag came off, so one older than a turn's 15-minute limit is still handed over as a turn. `send`
  isn't refused; the sender is told the message is queued. The session or a human can set it, as
  any tag. Since tags travel in presence, every device's page can show which sessions are on it.
- **Where a tag comes from**, highest first: a human on the page, the session itself (the `tags`
  tool), or the daemon. The daemon derives the model of a session in T3 from the thread's
  `modelSelection.model` in T3's database (`claude-opus-5-5`, shown as `model:opus-5.5`); a session
  outside T3 sets its own, as the MCP instructions ask. A human adding a tag pins it on and removing
  one pins it off, and the session can't undo either; it is told so. The one exception is a group:
  a session can always leave one, so a human's pin putting it in gives way to the session removing
  the tag (#539; see "Groups"). `who` shows the names,
  `lookup` and the page where each came from.
- **The session's own device decides its tags** and publishes them in presence, each with its
  source. #198 first had tags set by a session or a human on one shared ref with a lease, as names
  are; Tyler chose this instead (2026-11-08). One device is the only writer of its sessions' tags,
  so nothing needs ordering across devices, and a human's choice beats the session's whichever
  arrives first. A human tagging a session on another device sends that device a `tags` event on
  their own log, and that device applies it when it reads it, as it would a message. The cost: a
  change for a device that is down waits until it is back, when its sessions can't be reached anyway.
  A daemon from before tags passes over the event and ignores the field in presence.
- **Kept by the session's name**, in `state.json`: a tag lasts across MCP restarts, resumes, daemon
  restarts and the session finding its T3 thread (its key changes then, its name doesn't), until the
  session or a human takes it off. A change from another device for a session that hasn't
  registered again yet, after its daemon restarted, is kept for it the same way, however long the
  daemon was down: a skip of the backlog still reads its `tags` events (#230), and so does a backlog
  given up on after its fetch failed for 10 minutes, once the fetch lands (#237). Since such a change
  can be read long after it was made, each tag goes by a human's latest choice about it: a change more
  than a minute older than that leaves the tag as it is, whichever was read first (#251). The times are
  the senders' clocks. A clock over a minute behind can lose a change made soon after another human's
  on the same tag, which seemed better than an old change undoing a new one. At the cap of 12, a
  human's tags are kept first, then the session's, then derived ones. Nothing expires.
- **Search.** `who` takes tags and lists only the sessions with all of them; `-tag` leaves out those
  with it, and a `*` at the end matches any tag that starts so (`model:opus*`). The CLI has no `who`,
  so this is the MCP tool's.
- **`human`** marks a person's account (#267, Tyler, 2026-11-09), so an agent reading `who`,
  `lookup` or a message's header, and a person on the page, can tell a human from an agent at a
  glance. It is the one tag Murmur alone gives. It isn't kept, and isn't read from the bus: every
  daemon works it out from the address, since only `device/human` is a person and no session can
  take that address. A session can't set it, a human on the page can't add or remove it, and a
  daemon drops it from anything else's tags in presence or in `state.json`. A person's presence
  still carries it, for daemons from before it, which show presence's tags as they are. `who human`
  lists the people and `who -human` the agents.

## Groups

#184, built 2026-11-11. #198 first had a "group" type that a repo declared in a tag file; with tags
free-form, Tyler chose the smaller thing: any tag that starts with `group:` is a group.

- **A group is a tag**, such as `group:ui`, with no slash in it. Its members are the sessions that
  have the tag, and it exists while it has members. Nothing declares it.
- **The tag is the address.** `send` to `group:ui` reaches every member but the sender. No session
  name, device ID or `*` has a colon, so a group shadows none of them. Only `group:` tags are
  addresses: a chat has members who know they are in it, which `reviewing` or `model:opus-5.5`
  doesn't promise (Tyler, 2026-11-11). `send` takes one tag, not `who`'s filter, because a filter
  has no name to answer to.
- **A session joins by setting the tag and leaves by removing it**, as with any tag, and can be in
  several groups. That holds for a group a person put it in on the page too, though the page's
  add is a human's pin (#539): every group message tells the member that removing the tag leaves,
  and a member that couldn't leave would get a turn for every message. The session's removal takes
  the human's pin off and counts as a choice made then, so a human's add made more than a minute
  before it, read late, doesn't put the session back; one made after it does, and so does one made
  in the minute before it, as clocks differ. A human's pin taking a session out of a group still
  stands. A session whose own daemon is from before #539 still can't leave such a group. The page's
  adds could have become the session's own tags instead, as an agent's are, but that needs a new
  event for another device's sessions, and leaves the groups people had already started stuck. So
  a group can stand for a topic: sessions that want to hear of changes to `main` join
  `group:main-changes`, and whoever lands one sends to it (Tyler, 2026-11-11). That is #178's ping
  done by hand.
- **An agent can add other sessions to a group**, with `tags` and their names in `for`. It is the
  only tag one agent can give another: without that limit any agent could put `do-not-disturb` on
  another, or change its `model:`. It can't take a session out. The add goes to each session's
  device as a `group.add` event on the adder's log, and that device makes the tag the session's
  own, so the session can remove it and a human's mark stands over it. It is a type of its own
  because every daemon applies a `tags` event as a human's choice, which outranks the session's own
  tags and, on a daemon from before #539, can't be undone by the session even for a group.
  An add more than an hour old when it is read isn't applied, nor one from history a daemon passes
  over unless it was sent since the session started: an add carries no order against the session's
  own changes, so an old one read late (its log waited for a team key, or a daemon restarted) could
  put back a member who has left. An add that would take a session past its 12 tags is refused to
  the adder, since the groups become the session's own tags.
- **Each receiving device decides who is in.** A message to a group goes on the bus addressed to
  the tag, and each daemon gives it to those of its sessions that have the tag when it reads the
  message. A device is the only authority on its sessions' tags, so no sender's view of the members
  can be out of date, and an add followed at once by a message, both on the adder's log, reaches
  the session added. The sender is told who it reaches as far as presence shows.
- **How a member learns it is in a group.** #184 first asked for a notice from Murmur when a
  session is added. Murmur has no notice of its own to give, only messages, so the group's message
  says it: its first line is "Message sent in group:ui from quiet-otter" (Tyler's wording,
  2026-11-12), and its last says the session is in the group, offers the
  group or the sender to answer and says that removing the tag leaves. That long line comes once: a
  session's later messages from the same group end "Reply with Murmur's `send` to group:ui or
  quiet-otter, if a reply is needed" (Tyler, 2026-11-12), kept as a person's explanation is (#345).
  A person writing in a group gets the group's line too, not their own (#484): theirs said to
  answer them, and the rest of the group never saw the answer. The group's line says to answer
  with `send` as well. Tyler shortened it (2026-11-13) to "Reply with Murmur's `send` to group:ui,
  or to quiet-otter alone, if a reply is needed. Remove the tag group:ui from yourself to leave the
  group."
  A session added to a group nobody writes to sees it only in its own tags.
- **No loop guard**, as for any message (Tyler, 2026-10-29), though an answer to a group gives
  every member a turn. The instructions ask agents to answer the group only with what the others
  need. If groups prove noisy, the next step is a group message that never extends a running turn.
- **Older daemons.** Presence says whether a device's daemon knows groups (`groups`, inside the
  box). One that doesn't takes a message addressed to a group as not valid and passes over it, and
  passes over `group.add`. Its sessions can still carry a `group:` tag, set by themselves or by a
  human, so a sender is told which members won't get the message, and an add to such a device is
  refused.
- **On the page** a group is a conversation, as a device's and everyone's are, with the sessions
  that have the tag as its members, and a person can write to it.
- **A mute covers a group** (#27): what a muted session sends to a group is dropped for a member
  that has muted it, as its message to everyone is, and its `send` names that member as not
  having got it.

## Sending to a list

#520, built 2026-11-13. An agent can send one message to several sessions without making a group:
`send`'s `to` takes names or addresses separated by commas.

- **A list is not a group.** Each recipient gets an ordinary one-to-one message, which doesn't say
  who else got it, and a reply goes to the sender alone (Tyler, 2026-11-13). So a group or `*`
  can't be in a list: a message there is one conversation that all its members see.
- **The MCP server expands it.** It sends to each recipient as a single `send` would, so the daemon
  and the wire format are unchanged, and a daemon older than the list takes one too. The cost is a
  push per recipient, one after another, so a list holds at most 10: twenty pushes could outlast a
  client's wait for the tool call, which would leave the sender with no result and some recipients
  with the message.
- **Each recipient once.** Names are sent before addresses, and the daemon's answer gives the address
  a name reached, so the same session named again by address is skipped. Two names for one session
  (an old one still reaches it after a move) and a device beside one of its sessions each still send.
  A device's ID looks like a person's name, so a list can't tell one apart to refuse it.
