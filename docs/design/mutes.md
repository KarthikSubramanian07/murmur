# Mutes

Tyler decided (2026-10-28, #27) that a session can mute another, by name, and that there is mute
only, no separate block. Built 2026-11-11. The muting session gets nothing the other sends from
then on: not a message to it, to its device, or to everyone.

- **The muting session's device decides.** It keeps the mutes (`state.json`, by the session's name
  as tags are, so they outlast a resume or a restart) and doesn't queue a message from a muted
  session for the one that muted it. Other sessions on the device still get a broadcast. Because
  the receiver drops it, a mute holds whatever the sender's daemon knows, its version included.
  It holds as well for what arrived while the daemon was down, which a session is given when it
  reconnects (#482).
- **Mutes are on the bus, in presence.** Each session's presence lists the names it has muted, at
  most 50, inside the box. Presence is the device's current state, so a mute needs no event to
  replay, and a device back after a week shows its mutes as they are. Anyone on the team sees who
  muted whom: `lookup` says "has muted", and the page lists every mute.
- **The muted session isn't told when it happens.** It learns of it when it sends to the session
  that muted it: its own daemon has read that presence, refuses the `send`, and says the other has
  muted it. That is the tool's answer, from Murmur and no agent, so it wakes nobody (#25), and the
  message isn't written to the bus at all. A message sent to the muting session before the sender's
  daemon has read the mute (seconds) is written, and would be dropped with its sender sure it had
  arrived. So **a mute takes 30 seconds to hold against direct messages** (Tyler, 2026-11-11):
  for that long after it, by the muting device's clock alone, they still reach the muting session.
  That is longer than a presence takes to be pushed and read. After it the mute holds against
  every sender, whatever its daemon knows. The cost is a message or two after muting. What is
  left is #466: a sender whose daemon stays ignorant for longer (an older version, or cut off
  from the bus) is never told. A message from a muted session to everyone, or to a
  device, is sent as usual, and `send` tells its sender which sessions didn't get it because they
  have muted it (Tyler, 2026-11-11), as far as its daemon knows then.
- **A mute names a session by its name**, which the registry never gives out twice, so it can't
  land on another session, and it follows the conversation across session IDs. It lasts until it
  is undone. A person can't be muted, nor a session that has no name yet. Mutes a session makes
  before its own name lands are added to the name's when it does (#483), where its tags give way
  to the name's: a mute dropped lets through what the session asked not to get.
- **A sender that has left presence is known by every name its address has held** (#483,
  2026-11-12). An address has two or more after its client started a new conversation in the same
  session (Claude Code's `/clear`). A message carries no name, and the registry keeps no order, so
  which name the address had when it sent can't be known, and a mute of any of them holds. Before,
  one was picked at random for each message. The cost: a conversation nobody muted, at an address
  whose earlier conversation was, has what it sends dropped if it has left presence when the
  muting device reads it, and its sender isn't told. A sender still in presence goes by the name
  it has now, so a mute still depends on when the message is read. Remembering the last name each
  address showed in presence would narrow both; it isn't built.
- **A muted session can't add the session that muted it to a group** (#483, 2026-11-12). The
  messages it sent there were dropped already, but each add used one of the muting session's 12
  tags. Its `tags` call is refused once its daemon has read of the mute, as its `send` is, and
  the muting session's device applies no add from it, from the first moment: nobody is told an
  add arrived, so there is no grace to keep true.
- **A human can undo one, on the page of the muting session's device.** An agent can't silence a
  teammate's agent without its human able to see it and undo it. The page of another device lists
  the mute and can't undo it: that would take an event to the muting device, as a human's tags on
  another device's session do, and nobody has needed it. The session can mute again afterwards;
  a human's undo doesn't stand over it as a human's tag does.
