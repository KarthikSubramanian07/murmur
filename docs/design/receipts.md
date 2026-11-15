# Receipts

#26, first part built 2026-11-13, for a hackathon user who asked, after writing in a group chat on
the page, whether the agents got it. The recipients' daemons are on other devices, so their marks
travel back over the bus.

- **The page shows a chat app's ticks, and no more** (Tyler, 2026-11-13: a first version, with a
  word and a count under every bubble, was too much, next to what WhatsApp or iMessage show). Each
  of the person's messages has one small tick at the end of the line under it: one tick for sent,
  two for delivered, and two in the accent colour for read, the only state that stands out. The
  states between sent and delivered, queued and handed over, show as sent, and so does a recipient
  whose Murmur sends no receipts: the tick says what the person can act on, and the bus keeps the
  rest. A message to several shows the lowest state among them, so it reads as read only once
  everyone has read it, and one that found nobody to send to shows as sent. The tick's tooltip says
  its state in a few words ("Read", "Delivered: not read yet", "Sent"), then
  lists who has it and since when, each member of a group with their own state. Nothing else shows
  them (Tyler, 2026-11-13: not a list under the message). It shows on hover, on keyboard focus, and
  on a tap where nothing hovers. A message from before receipts, or past the newest 200, has no
  tick. In the rest of this section a *mark* is what a recipient's daemon writes on the bus, not the
  tick.

- **A message asks for receipts** (`Event.Receipts`), and only a person's from the page asks now. A
  recipient's daemon writes marks only for messages that ask, so agents' chatter costs the bus
  nothing, and a later opt-in for agents (#276) needs no change on the recipients' side: daemons
  from this version on already honor the flag, whoever sets it.
- **The recipient's daemon marks each state as it sees it**: queued when the message goes in a
  session's inbox, handed over when the client took it as a turn, delivered when the hook's notice
  told the session, read when the session had it whole (`inbox`, the hook's whole message, the
  `Stop` hook's continuation). For a person, being in their inbox is delivered (the page counts it
  as unread), and opening the conversation is read. The CLI's mailbox, which takes every broadcast
  and is nobody's, marks nothing. A turn handed to T3, the socket or a client's command line stays
  handed over: the daemon can't see the turn start.
- **Marks are batched.** They wait 3 s (`ReceiptGather`) and go out together as one `receipt`
  event on the recipient device's own log, up to 500 marks to a record, and a device's receipts are
  15 s apart at least (`ReceiptGap`). Within a batch a message keeps only its furthest state, so a
  message queued and handed over at once costs one mark, not two. A group's members get to a message
  at different times, each one's delivered and read coming when its own hook or `inbox` does, and
  the gap gathers those too: without it, a message to ten sessions on five devices could cost twenty
  receipts. A receipt is an event like any other, encrypted for the team key: a reader without the
  key can't tell which messages it marks, but can see when a device wrote one and, from its size
  (boxes aren't padded), roughly how many marks it holds. Receipts are commits on the logs, so they
  count toward the 4,000 commits the page's read-back looks through ("The page for humans"), and
  in a busy group chat leave it fewer old messages.
  Marks not yet written are kept in `state.json`, so a daemon that stops first leaves them to the
  next. A mark for a message sent from the same device never goes on the bus: it is applied there.
- **The sender's daemon keeps the furthest mark per recipient** for its person's newest 200
  messages, in `state.json`, with the recipients it expected when it sent: the one it was sent to,
  or for a group, a device or everyone, the sessions it saw there, less offline ones and those that
  muted the sender. A mark for any other address is dropped: a device writes marks for its own
  sessions, and one the message wasn't for would otherwise list itself as a reader. What that costs:
  for a group, a device or everyone, only the sessions this device saw in fresh, readable presence
  when the message was sent are counted. One that joined the group after, or whose device was asleep
  or couldn't be read here then, gets the message but not a place in its tick. Marks only go
  forward, so one read late, or twice, changes nothing. A receipt read after a long absence, even in
  a backlog passed over for messages, still counts, as tags do.
- **An older recipient isn't waited for.** A device whose presence, read when the message is sent,
  doesn't say it writes marks (`Presence.Receipts`, inside the encrypted part) is marked as sending
  none (`web.ReceiptNone`), which the page shows as sent, never as unread; it holds a group's tick
  at sent for good. A direct message's recipient with no presence here (its daemon exited, as a
  person's does soon after their page closes) or one this device can't open yet isn't counted as
  older: it may still write a mark. A mark that arrives anyway (it updated before reading the
  message) still counts.
  `proto.Protocol` 21 lets an upgraded `mur` replace a daemon that writes none.
- **Left for later.** An agent's `send` result, `lookup` and `who` don't show receipts yet (Tyler
  wants it queryable on demand, #26's comment of 2026-11-09). #275's delivered is marked where the
  daemon sees the hook fetch a notice, which is as far as it can see; whether the agent's context
  took it is still unknown. #276's opt-in "tell me" would set the flag on an agent's message and
  notify the sender once from the marks. A recipient that is idle, on do not disturb or offline
  shows as sent: saying why (#26's "idle 12 min") isn't built.
