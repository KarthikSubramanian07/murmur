// Mock data for the Briefing (#183): what the humans want to know, in their words.
//
// Decisions: things agents settled between themselves over Murmur. In this draft they are written
// by hand; for real, an agent would mark a message as a decision when it sends it (a `decision`
// field on send, with a one-line why), so nothing has to guess at them afterwards.
// Workstreams: one per goal (an issue, its branch and PR), with the sessions on it and a sentence
// on where it stands, drawn from their statuses, commits and messages.
const MIN = 60 * 1000;
const now = Date.now();
const iso = (minAgo) => new Date(now - minAgo * MIN).toISOString();

export const brief = {
  lastLooked: iso(150),
  decisions: [
    {
      id: "d1", at: iso(1.5), stream: "receipts",
      what: "The daemon will keep when each session's last turn ended, and receipts will read it from there.",
      why: "Receipts want to tell a sender how long the recipient has been idle, and wake-ups already needs that time.",
      by: ["solar-wren", "mellow-yak"], source: "Receipts can say \"idle 12 min\"", messages: 2,
    },
    {
      id: "d2", at: iso(5), stream: "remove-claims",
      what: "A session's status (Doing) stays in SessionView. Only Claims is removed.",
      why: "The web page reads Doing for every session; nothing reads Claims.",
      by: ["brave-heron", "quiet-otter"], source: "Session view types", messages: 4,
    },
    {
      id: "d3", at: iso(18), stream: "ls-remote",
      what: "Devices keep polling GitHub every 10 seconds.",
      why: "4 devices ran for 30 minutes at that rate with no rate limiting from GitHub.",
      by: ["rapid-ibis"], source: "No rate limit hit at 6 ls-remote/min/device", messages: 1, broadcast: true,
    },
    {
      id: "d4", at: iso(46), stream: "session-names",
      what: "The page shows nothing for a session until its name has landed, and keys everything on names.",
      why: "Names are never reused; a session without one isn't on the bus yet, for a second or two.",
      by: ["lucid-badger", "quiet-otter"], source: "What does a session's name look like before it lands?", messages: 4,
    },
    {
      id: "d5", at: iso(58), stream: "macos-ci",
      what: "brave-heron will fix the macOS TestRestart failure, after the claims removal (#24) merges.",
      why: "The interrupt seems to reach the whole process group, which brave-heron is already changing.",
      by: ["brave-heron", "dusty-lynx"], source: "macOS run is red", messages: 2,
    },
    {
      id: "d6", at: iso(80), stream: "session-names",
      what: "Session names move to a new shared ref, refs/murmur/names. `send` takes a name instead of device/session.",
      why: "Two-word names are easier for agents and people than device/session addresses.",
      by: ["lucid-badger"], source: "Names are moving to refs/murmur/names", messages: 1, broadcast: true, big: true,
    },
    {
      id: "d7", at: iso(117), stream: "remove-claims",
      what: "Claims are deleted entirely: claims.go and the claim and release tools go.",
      why: "No agent was using them, and wake-ups confirmed it doesn't read them.",
      by: ["brave-heron", "solar-wren"], source: "Claims are going away under you", messages: 2, big: true,
    },
  ],
  streams: [
    { id: "remove-claims", title: "Remove claims", issue: 24, branch: "remove-claims", who: ["brave-heron"], now: "Deleting claims from the daemon and the MCP tools. Its tests have failed three runs in a row for the last 9 minutes.", tone: "error", commits: 3 },
    { id: "session-names", title: "Two-word session names", issue: 20, pr: 31, branch: "session-names", who: ["lucid-badger", "velvet-moth"], now: "Names are taken by compare-and-swap, and the word lists are in. velvet-moth is on do not disturb.", tone: "ok", commits: 4 },
    { id: "web-page", title: "Three-column web page", issue: 179, pr: 36, draft: true, branch: "web-page", who: ["quiet-otter"], now: "Building the settings view. Waiting on velvet-moth to say whether the word lists are final.", tone: "warn", commits: 4 },
    { id: "wake-ups", title: "Wake idle sessions", issue: 25, pr: 33, draft: true, branch: "wake-ups", who: ["solar-wren"], now: "Finding out whether Claude Code's messaging socket can start a turn in an idle session.", tone: "ok", commits: 1 },
    { id: "receipts", title: "Read receipts", issue: 26, pr: 35, branch: "receipts", who: ["mellow-yak"], now: "Agreed with solar-wren where idle time comes from; quiet for 12 minutes since.", tone: "ok", commits: 2 },
    { id: "ls-remote", title: "GitHub rate limits", issue: 15, branch: "fix/ls-remote-rate-limit", who: ["rapid-ibis"], now: "Measured the limit; now waiting on the Mac mini to rerun a test it touched.", tone: "warn", commits: 1 },
    { id: "macos-ci", title: "Tests on macOS", branch: "macos-ci", who: ["dusty-lynx"], now: "Its device has been offline for 47 minutes, with a request to rerun TestRestart waiting.", tone: "off", commits: 1 },
  ],
  unexplained: [{ who: "amber-finch", why: "On main with no status, idle 26 minutes. It read lucid-badger's question about refs/murmur/names and didn't answer." }],
};
