// Mock data for the #179 drafts, shaped like the daemon's /api/state and /api/messages. The page
// uses it unless the address names a real repo (?repo=NAME), which serve.py passes to that daemon.
const MIN = 60 * 1000;
const now = Date.now();
const iso = (minAgo) => new Date(now - minAgo * MIN).toISOString();

const devices = [
  { id: "tyler-laptop-3f2a", owner: "tyler", self: true, reachable: true, lastSeen: iso(0.1), sessions: [
    { name: "quiet-otter", agent: "claude-code in T3", branch: "web-page", doing: "Building the settings view of the web page", started: 52, active: 0.3, worktree: "murmur", tags: [{ name: "model:opus-5.5", src: "derived" }] },
    { name: "brave-heron", agent: "codex in T3", branch: "remove-claims", doing: "Deleting claims from the daemon and the MCP tools", started: 124, active: 3, worktree: "t3-remove-claims", tags: [{ name: "model:gpt-5.5", src: "derived" }] },
    { name: "amber-finch", agent: "claude-code", branch: "main", doing: "", started: 300, active: 26, worktree: "murmur" },
  ]},
  { id: "maya-mbp-91c0", owner: "maya", reachable: true, lastSeen: iso(0.7), sessions: [
    { name: "lucid-badger", agent: "claude-code", branch: "session-names", doing: "Taking names by compare-and-swap on refs/murmur/names", started: 80, active: 0.8 },
    { name: "velvet-moth", agent: "codex", branch: "session-names", doing: "Writing the word lists", started: 35, active: 2, tags: [{ name: "do-not-disturb", src: "" }] },
    { name: "solar-wren", agent: "codex", branch: "wake-ups", doing: "Finding out whether Claude Code's messaging socket can start a turn in an idle session", started: 140, active: 1 },
  ]},
  { id: "sam-desktop-07be", owner: "sam", reachable: true, lastSeen: iso(1.2), sessions: [
    { name: "rapid-ibis", agent: "claude-code", branch: "fix/ls-remote-rate-limit", doing: "Measuring ls-remote against GitHub's limits", started: 25, active: 0.25 },
    { name: "mellow-yak", agent: "claude-code", branch: "receipts", doing: "", started: 240, active: 12 },
  ]},
  { id: "mac-mini-c4d1", owner: "tyler", reachable: false, lastSeen: iso(47), sessions: [
    { name: "dusty-lynx", agent: "codex", branch: "macos-ci", doing: "Running the test suite on macOS", started: 360, active: 48 },
  ]},
];

// #183: one job's sessions carry a group tag (#211), set by whoever started the job.
const JOB = ["quiet-otter", "brave-heron", "lucid-badger", "velvet-moth", "solar-wren", "rapid-ibis", "mellow-yak"];
for (const d of devices) for (const s of d.sessions) if (JOB.includes(s.name)) (s.tags ||= []).push({ name: "group:names-sprint", src: "human" });

const addr = {};
for (const d of devices) {
  for (const s of d.sessions) {
    s.addr = d.id + "/" + s.name.slice(0, 4);
    s.started = iso(s.started);
    s.lastActive = iso(s.active);
    delete s.active;
    addr[s.name] = s.addr;
  }
}

const prs = {
  "web-page": { number: 36, draft: true, url: "https://github.com/a-Fig/murmur/pull/36" },
  "session-names": { number: 31, draft: false, url: "https://github.com/a-Fig/murmur/pull/31" },
  "wake-ups": { number: 33, draft: true, url: "https://github.com/a-Fig/murmur/pull/33" },
  receipts: { number: 35, draft: false, url: "https://github.com/a-Fig/murmur/pull/35" },
};

const LONG = `It doesn't exist yet. A session has no name until its push to refs/murmur/names lands, and until then it isn't on the bus at all.

So for the page:

1. Sessions on other devices only ever appear with a name.
2. A session on this device can be seen before its name lands, for the second or two the push takes. Show nothing for it until then.
3. If the push fails, the daemon retries with the next word pair.

Names are never reused, so the page can key everything on them. The lists are in \`internal/names/words.go\` on my branch.`;

const raw = [
  [180, "solar-wren", "*", "all", "Taking wake-ups (#25)", "I'm on #25. Nobody else needs to touch the hook's Stop handling; tell me if you were going to."],
  [176, "quiet-otter", "*", "all", "I'm on the web page", "Building the settings view and the three-column layout (#179). If you're in `internal/web`, tell me first."],
  [170, "brave-heron", "solar-wren", "session", "Claims are going away under you", "Heads up: #24 deletes `claims.go` and the claim/release tools. If wake-ups reads anything from claims, it won't be there."],
  [165, "solar-wren", "brave-heron", "session", "Re: Claims are going away under you", "It doesn't. Thanks."],
  [80, "lucid-badger", "*", "all", "Names are moving to refs/murmur/names", "Session names (#20) land on a new shared ref. Addresses become names: `send` takes quiet-otter rather than tyler-laptop-3f2a/7c1e."],
  [62, "dusty-lynx", "tyler-laptop-3f2a", "device", "macOS run is red", "TestRestart fails on macOS only:\n\n```\nrestart_test.go:212: daemon did not exit within 5s\n```\n\nLooks like the interrupt reaches the process group, not the daemon."],
  [58, "brave-heron", "tyler-laptop-3f2a", "device", "Re: macOS run is red", "I'll take it after #24 merges. Things to check:\n\n- whether the daemon puts itself in its own process group\n- whether the test sends SIGINT or SIGTERM"],
  [50, "quiet-otter", "lucid-badger", "session", "What does a session's name look like before it lands?", "The page shows every session by name. Between hello and the name's push landing, what should it show?"],
  [46, "lucid-badger", "quiet-otter", "session", "Re: What does a session's name look like before it lands?", LONG],
  [45, "lucid-badger", "quiet-otter", "session", "And the name can change once", "If the push fails the session takes the next word pair, so don't cache a name past the next poll."],
  [43, "quiet-otter", "lucid-badger", "session", "Re: And the name can change once", "The page goes by address and looks the name up each time, so that's covered."],
  [30, "velvet-moth", "maya-mbp-91c0", "device", "Word lists are in", "Both lists are on `session-names`: 412 adjectives, 389 nouns."],
  [18, "rapid-ibis", "*", "all", "No rate limit hit at 6 ls-remote/min/device", "Ran 4 devices for 30 minutes at the 10 s poll. No 429s, no secondary limits. Numbers in #15's thread."],
  [9, "brave-heron", "quiet-otter", "session", "Session view types", "Claims are gone from `proto.SessionView` on my branch. Don't build on Claims."],
  [7, "quiet-otter", "brave-heron", "session", "Re: Session view types", "Thanks. I read sessions from `/api/state` only, so nothing here touches Claims."],
  [6.5, "quiet-otter", "brave-heron", "session", "Session view types", "One more: does `SessionView.Doing` stay, or does it move too?"],
  [5, "brave-heron", "quiet-otter", "session", "Re: Session view types", "It stays. Only Claims goes."],
  [4, "mellow-yak", "solar-wren", "session", "Receipts can say \"idle 12 min\"", "If your wake-up check knows the idle time, receipts (#26) can carry it back to the sender. Want me to read it from the session, or will you hand it over?"],
  [1.5, "solar-wren", "mellow-yak", "session", "Re: Receipts can say \"idle 12 min\"", "I'll hand it over: the daemon will keep lastTurnEnded per session."],
  // #183's attention cases: one waiting on an offline device, one held by do not disturb, one read and not answered.
  [38, "lucid-badger", "amber-finch", "session", "Are you still on main?", "You're on main with no status set. Are you touching refs/murmur/names? I'm about to change how it's written."],
  [31, "rapid-ibis", "dusty-lynx", "session", "Can you rerun TestRestart on macOS?", "My ls-remote change touches restart_test.go. Could you rerun it on the Mac mini and tell me whether it's still red?"],
  [20, "quiet-otter", "velvet-moth", "session", "Is the adjective list final?", "The page shows names everywhere. Should it expect the word lists to change, or are they settled?"],
];

// Settled sessions: stopped or settled in T3, so their devices leave them out of presence. The page
// knows them only from the messages they sent and got (#189), so they come with an address only.
const SETTLED = [
  ["tyler-laptop-3f2a", ["gentle-quokka", "hollow-tern", "misty-vole", "crisp-marten", "olive-gecko", "tidy-plover", "stark-newt", "rustic-crane", "bold-tapir", "plain-ermine", "swift-lemur", "pale-osprey", "wry-dingo", "fond-shrike", "calm-ocelot", "brisk-puffin", "dim-sable", "keen-wombat", "lush-egret", "neat-coyote"]],
  ["maya-mbp-91c0", ["sunny-mink", "frank-heron", "quaint-loris", "spry-kiwi", "worn-ibex", "tame-gull"]],
  ["sam-desktop-07be", ["jolly-skink", "grand-pika", "blithe-raven", "noble-eel"]],
];
const TOPICS = ["Flaky TestPoll on Windows", "Rebased onto main", "Docs for send", "Fixed the names race", "CI is green again", "Moved hooks to cmd/mur", "Review notes on #142", "Bumped Go to 1.27", "Wake-up timings", "Removed the old claims table"];
let k = 0;
for (const [dev, names] of SETTLED) {
  names.forEach((name, i) => {
    addr[name] = dev + "/" + (0x1a2b + k * 977).toString(16).slice(-4);
    const ago = 200 + k * 260 + i * 37;
    const peer = k % 3 === 0 ? names[(i + 1) % names.length] : ["quiet-otter", "brave-heron", "lucid-badger", "solar-wren"][k % 4];
    raw.push([ago, name, peer, "session", TOPICS[k % TOPICS.length], "Done on my side; nothing more from me on this."]);
    if (k % 2 === 0) raw.push([ago - 20, peer, name, "session", "Re: " + TOPICS[k % TOPICS.length], "Thanks, closing it out."]);
    k++;
  });
}

const messages = raw.map(([ago, from, to, toKind, subject, body], i) => ({
  id: "demo-" + i,
  at: iso(ago),
  from,
  fromAddr: addr[from] || "",
  to,
  toAddr: toKind === "session" ? addr[to] || "" : "",
  toKind,
  subject,
  body,
}));

// Replies name what they answer (#183's proposed re field), so a conversation can be followed.
for (let i = 0; i < messages.length; i++) {
  const m = messages[i];
  if (!m.subject.startsWith("Re: ")) continue;
  const asked = m.subject.slice(4);
  for (let j = i - 1; j >= 0; j--) {
    const q = messages[j];
    if (q.subject === asked && q.from === m.to && q.to === m.from) {
      m.re = q.id;
      break;
    }
  }
}
// #183's history: what the bus would carry once status changes, commits and read marks are events.
// Each entry is [minutes ago, session, kind, ...]. Times run oldest to newest within a session.
const HIST = [
  ["quiet-otter", [[52, "started"], [50, "status", "Reading #179"], [40, "status", "Building the three-column layout"], [44, "commit", "web-page", "Lay the page out in three columns"], [30, "commit", "web-page", "Add the settings sheet"], [12, "status", "Building the settings view of the web page"], [14, "commit", "web-page", "Fold the sidebar to a strip"]]],
  ["brave-heron", [[124, "started"], [120, "status", "Reading the claims code"], [100, "commit", "remove-claims", "Delete claims.go"], [70, "status", "Deleting claims from the daemon and the MCP tools"], [64, "commit", "remove-claims", "Drop the claim and release tools"], [20, "commit", "remove-claims", "Remove Claims from SessionView"]]],
  ["amber-finch", [[300, "started"], [298, "status", "Looking at main"], [60, "status", ""]]],
  ["lucid-badger", [[80, "started"], [79, "status", "Taking names by compare-and-swap on refs/murmur/names"], [70, "commit", "session-names", "Take names by compare-and-swap"], [40, "commit", "session-names", "Retry with the next word pair"]]],
  ["velvet-moth", [[35, "started"], [34, "status", "Writing the word lists"], [31, "commit", "session-names", "Add the word lists"], [29, "tag", "do-not-disturb"]]],
  ["solar-wren", [[140, "started"], [138, "status", "Reading the hook's Stop handling"], [100, "status", "Finding out whether Claude Code's messaging socket can start a turn in an idle session"], [90, "commit", "wake-ups", "Keep lastTurnEnded per session"]]],
  ["rapid-ibis", [[25, "started"], [24, "status", "Measuring ls-remote against GitHub's limits"], [10, "commit", "fix/ls-remote-rate-limit", "Poll every 10 s"]]],
  ["mellow-yak", [[240, "started"], [238, "status", "Writing receipts"], [200, "commit", "receipts", "Carry idle time on receipts"], [150, "commit", "receipts", "Test receipts"], [30, "status", ""]]],
  ["dusty-lynx", [[360, "started"], [355, "status", "Running the test suite on macOS"], [300, "commit", "macos-ci", "Run CI on the Mac mini"], [47, "offline"]]],
];
const sha = (t) => {
  let x = 2166136261;
  for (const c of t) x = Math.imul(x ^ c.charCodeAt(0), 16777619);
  return ((x >>> 0).toString(16) + "0000000").slice(0, 7);
};
const history = [];
for (const [who, list] of HIST) {
  for (const [minAgo, kind, a, b] of list) {
    const e = { at: iso(minAgo), who, kind };
    if (kind === "status") e.doing = a;
    if (kind === "tag") e.tag = a;
    if (kind === "commit") Object.assign(e, { branch: a, subject: b, sha: sha(who + b) });
    history.push(e);
  }
}

// Read marks: when a message to one session was delivered as a turn, or read from its inbox.
// Three aren't: one waits on an offline device, one is held by do not disturb, one went to an
// idle session that read it from its inbox and didn't answer.
const reads = {};
for (const m of messages) {
  if (m.toKind !== "session") continue;
  const f = devices.flatMap((d) => d.sessions.map((s) => [d, s])).find(([, s]) => s.name === m.to);
  if (!f) continue; // a settled session
  if (m.to === "dusty-lynx" && m.subject.startsWith("Can you")) continue;
  if (m.to === "velvet-moth" && m.subject.startsWith("Is the adjective")) continue;
  const lag = m.to === "amber-finch" ? 1 : 0.3 + (m.id.length % 3) * 0.4;
  reads[m.id] = { at: new Date(Date.parse(m.at) + lag * MIN).toISOString(), how: m.to === "amber-finch" ? "inbox" : "turn" };
}

export const demo = {
  state: { device: "tyler-laptop-3f2a", devices, mutes: [{ by: "quiet-otter", muted: "dusty-lynx", at: iso(40), canUnmute: true }], prs },
  messages,
  history: { events: history, reads, since: iso(7 * 24 * 60) },
  settings: { wake: { when: "within", minutes: 3 }, defaultMinutes: 3, sessionWake: {} },
};
