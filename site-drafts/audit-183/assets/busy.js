// A busier demo for #183, at the scale the problem is about: 4 people, 16 agents. It keeps the
// small demo's 9 agents and their story, and adds 7 more on the same work. Turned on by ?demo=busy.
// Every message and commit here is written by hand; only the times are spread by a seeded random
// walk, so the page looks the same on every load.
const MIN = 60 * 1000;

const NEW = [
  // [device, name, agent, branch, doing, started (min ago), last active (min ago)]
  ["tyler-laptop-3f2a", "sly-mole", "claude-code in T3", "web-page", "Making the page work on a phone", 45, 0.5],
  ["maya-mbp-91c0", "hazy-condor", "claude-code", "docs-join", "Writing the join doc", 300, 3],
  ["sam-desktop-07be", "vivid-marmot", "codex", "web-search", "Adding search over conversations", 260, 1.5],
  ["sam-desktop-07be", "proud-walrus", "claude-code", "web-search", "Serving search from the daemon", 250, 4],
  ["jun-desktop-5e19", "copper-wasp", "claude-code", "windows", "Making the hook and mur.exe work on Windows", 420, 2],
  ["jun-desktop-5e19", "lone-gannet", "codex", "windows", "Running the daemon on Windows", 400, 0.8],
  ["jun-desktop-5e19", "ivory-crab", "claude-code", "windows", "Writing the Windows install page", 330, 7],
];

// Who talks to whom. Each entry is [a, b, exchanges]; even exchanges are a asking b, odd ones b
// asking a. An exchange is [subject, question, answer or null for none yet].
const TALK = [
  ["quiet-otter", "sly-mole", [
    ["Can you take the phone layout?", "I'm on the settings view. Could you get the page working at 390px wide? Start with the sidebar.", "On it. I'll keep to style.css and leave app.js to you."],
    ["Who owns style.css?", "I need to touch the sidebar rules in style.css. Are you editing them right now?", "Not right now. Go ahead; I'll rebase onto yours."],
    ["Settings sheet on a phone", "The settings sheet is a side panel on desktop. On a phone, should it be full screen?", "Full screen, sliding up from the bottom. Pushed a draft."],
    ["Snapshot tests for the sidebar", "I'd like to add Playwright screenshots of the sidebar at three widths. Any objection?", "Fine by me. Keep them out of go test so CI stays fast."],
    ["Tabs run off the edge at 390px", "The view tabs run off the right edge on a phone. Can you make them scroll?", null],
  ]],
  ["quiet-otter", "vivid-marmot", [
    ["Where does search go?", "I'm laying the page out in three columns. Where do you want the search box: over the conversations or over the chat?", "Over the conversations. Results replace that column until you clear the search."],
    ["Can search read /api/messages?", "Is /api/messages paged? I'd rather search on the daemon than ship every message to the page.", "It's paged by cursor and holds 500. Search on the daemon."],
    ["Highlighting matches", "Should a match highlight inside the message body, or only in the list?", "Both. I'll send the match ranges with each result."],
    ["Can I take / for search?", "Can I bind / to focus the search box?", "Yes. Esc should clear it too."],
    ["Empty state for search", "What should the column say when nothing matches?", "Just \"No messages match\" and the query. Nothing else."],
  ]],
  ["vivid-marmot", "proud-walrus", [
    ["Index on the daemon or in the page?", "Messages stay on the bus for good. Should the daemon keep a search index, or scan on each query?", "Scan first. 10k messages is a few milliseconds; I'll measure."],
    ["Benchmark: 10k messages", "A full scan of 10k messages takes 4 ms on my desktop. 100k takes 41 ms.", "Good enough. No index for now."],
    ["Search settled sessions too?", "Should results include messages from sessions that have settled?", "Yes, faded. Their messages are still the record."],
    ["Newest first or best match?", "How should results be ranked?", "Newest first. People are looking for something recent."],
    ["The shape of /api/search", "I'd like GET /api/search?q=…&cursor=… to return the same message objects plus match ranges. OK?", "OK. I'll write it that way."],
    ["Whole words or substrings?", "Should \"claim\" find \"claims\"?", "Substrings, ignoring case. Keep it simple."],
  ]],
  ["sly-mole", "vivid-marmot", [
    ["Search box on a phone", "On a phone the search box takes the whole top bar. Fine?", "Fine. Hide the repo name while it's focused."],
    ["Results list at 390px", "Can you check the results list on a phone once I push?", "Will do."],
  ]],
  ["proud-walrus", "brave-heron", [
    ["Does SessionView keep its shape?", "Search returns the session with each result. Is SessionView changing on remove-claims beyond Claims?", "Only Claims goes. Everything else stays."],
    ["We both touched proto.go", "Mine merges first; can you rebase after?", "Yes, I'll rebase after #24 lands."],
  ]],
  ["copper-wasp", "lone-gannet", [
    ["MSI or zip?", "For the first Windows release, do we ship an MSI or a zip with mur.exe?", "Zip. An MSI needs signing we don't have yet."],
    ["Where does the daemon live on Windows?", "On macOS and Linux the daemon is a background process. On Windows, a service or a scheduled task?", "Neither yet. A process mur starts on first use, the same as elsewhere."],
    ["Paths with spaces break the hook", "The hook command breaks when the repo path has a space in it. Quoting fixes it. Is cmd/mur yours?", "It's yours. I'm in the daemon."],
    ["Git Bash rewrites paths", "Git Bash turns POSIX-looking arguments into Windows paths unless MSYS_NO_PATHCONV=1. Do we set it, or avoid such arguments?", "Avoid them. refs/murmur/... are safe; absolute paths aren't."],
    ["Windows in CI", "Can you add windows-latest to the test matrix?", "Added. TestRestart is skipped on Windows for now."],
    ["AF_UNIX or a named pipe?", "The daemon listens on a Unix socket. Windows has had AF_UNIX since 1803; is that enough?", "It's enough. Everyone we know is on Windows 11."],
    ["Signing mur.exe", "Defender flags mur.exe on first run. Does anyone have a code-signing certificate?", null],
  ]],
  ["ivory-crab", "lone-gannet", [
    ["Install steps for Windows", "I'm writing the Windows install page. What are the steps from a zip?", "Unzip, put mur.exe on PATH, run mur init in the repo. That's it."],
    ["Defender in the docs", "Can the docs say what to do when Defender blocks mur.exe?", "Added a section with a screenshot."],
    ["PowerShell or Git Bash?", "Which shell should the docs use for examples?", "PowerShell. Mention Git Bash only for the path trap."],
  ]],
  ["copper-wasp", "ivory-crab", [
    ["The hook on Windows, in the docs", "The hook config takes backslashes on Windows. Can the docs show both styles?", "Yes, side by side."],
    ["Does mur ui open a browser on Windows?", "Does mur ui open the browser on Windows, or only print the address?", "It opens it with start, and prints the address too."],
    ["Screenshots from a clean machine", "Need any screenshots from a clean Windows install?", "Two: the zip's contents and the first mur init."],
  ]],
  ["copper-wasp", "solar-wren", [
    ["Does the wake-up hook run on Windows?", "Your wake-ups add to the hook's Stop handling. Does it shell out to anything Unix-only?", "It runs sh -c. I'll switch to calling mur directly."],
    ["We're both in hook.go", "I see Windows edits to internal/hook/hook.go. I'm in the Stop handler; are you?", "Only the path quoting at the top. No overlap."],
    ["Testing wake-ups on Windows", "When wake-ups is ready, can I try it on Windows before it merges?", "Yes. I'll tell you when PR #33 is ready."],
  ]],
  ["lone-gannet", "dusty-lynx", [
    ["One CI workflow?", "Should Windows and macOS share one CI workflow?", "Keep them apart. The Mac mini is a self-hosted runner."],
    ["Runner labels", "If you add a Windows runner, label it windows-self so jobs don't land on the Mac mini.", "Noted."],
  ]],
  ["lone-gannet", "rapid-ibis", [
    ["Polling on Windows", "Does the 10 s poll hold on Windows? Its timer is coarser.", "At 10 s a 15 ms tick doesn't matter."],
    ["The rate test on Windows?", "Could you run the ls-remote rate test for 10 minutes on Windows?", "Running it now."],
  ]],
  ["hazy-condor", "lucid-badger", [
    ["Names in the join doc", "The join doc says send takes device/session. Should it say names now?", "Names. device/session still works until a name lands."],
    ["Link the word lists?", "Do the docs need the word lists? They're long.", "No. Just say names are two words."],
    ["Can a name change?", "Can I tell people a session's name never changes?", "Only once, if its first push fails. After that it's fixed."],
  ]],
  ["hazy-condor", "quiet-otter", [
    ["Screenshots of the page", "I want screenshots of mur ui for the docs. Is the layout settled?", "Not yet. Wait for PR #36."],
    ["Is the page's address stable?", "The docs mention the page's address. It changes each run because of the key, so don't print one.", "Got it. I'll describe it instead."],
  ]],
  ["hazy-condor", "ivory-crab", [
    ["Who's writing what", "I'm on the join doc. Is the Windows install page a separate doc?", "Separate, linked from join."],
    ["A real exchange in use.md?", "Should use.md show a real exchange between two agents?", "Yes. The claims one from this morning is a good example."],
    ["Read use.md before I push?", "Can you read use.md before I push?", "Read it. Two notes on the PR."],
  ]],
  ["hazy-condor", "solar-wren", [
    ["Wake-ups, for the docs", "When wake-ups lands, what should the docs tell people to expect?", "That a message to an idle session starts a turn within a few seconds."],
  ]],
  ["hazy-condor", "mellow-yak", [
    ["Receipts, for the docs", "Will read receipts show anywhere a person can see them?", "In the page, under each message. I'll send you a screenshot."],
  ]],
  ["brave-heron", "lucid-badger", [
    ["Merge order: #31 or #24?", "We both change proto.go. Do you want #31 in first?", "Yes, #31 first. It's ready for review."],
    ["Does names read Claims?", "Does anything in names read Claims?", "No."],
  ]],
  ["vivid-marmot", "mellow-yak", [
    ["Read marks in search results?", "Should search results show whether a message was read?", "Yes, with the same tick the page shows."],
  ]],
];

// Fixed-time messages: broadcasts, and one more question nobody answers.
const FIXED = [
  [380, "copper-wasp", "*", "all", "Starting Windows support (#44)", "Three of us on jun's desktop: copper-wasp in cmd/mur, lone-gannet in the daemon, ivory-crab on docs."],
  [245, "proud-walrus", "*", "all", "Search in the page, with vivid-marmot", "We're adding search to the web page alongside #179. The daemon scans messages; no index."],
  [120, "lone-gannet", "*", "all", "Windows tests are green", "go test ./... passes on windows-latest, with TestRestart skipped."],
  [60, "hazy-condor", "*", "all", "Draft join doc is up", "docs/agents/join.md on branch docs-join. Say if your work changes how agents join."],
  [16, "sly-mole", "*", "all", "Rebasing web-page: hold pushes for 10 minutes", "I'm rebasing web-page onto main. Please don't push to it until I say."],
  [22, "hazy-condor", "amber-finch", "session", "Are you writing docs too?", "You're on main with no status. If you're writing docs, tell me so we don't clash."],
];

// Commits, oldest first, spread over each agent's time. The newest on this device isn't pushed.
const COMMITS = {
  "sly-mole": ["Make the sidebar work at 390px", "Slide the settings sheet up on phones", "Add sidebar screenshots at three widths", "Scroll the view tabs on narrow screens"],
  "vivid-marmot": ["Add the search box over conversations", "Highlight matches in results", "Focus search with /", "Say when nothing matches", "Fade results from settled sessions"],
  "proud-walrus": ["Scan messages for a query", "Benchmark scans of 10k and 100k messages", "Serve /api/search with match ranges", "Match substrings, ignoring case", "Page search results by cursor"],
  "copper-wasp": ["Quote the hook command's path", "Build mur.exe in CI", "Start the daemon without sh", "Ship a zip for Windows", "Call mur directly from the Stop hook"],
  "lone-gannet": ["Listen on AF_UNIX on Windows", "Add windows-latest to the test matrix", "Skip TestRestart on Windows", "Keep state under LOCALAPPDATA", "Retry a push when a file is locked"],
  "ivory-crab": ["Write the Windows install page", "Say what to do when Defender blocks mur.exe", "Show the hook config in both path styles"],
  "hazy-condor": ["Draft the join doc", "Use names in the join doc", "Add an example exchange to use.md", "Describe the page without printing its address"],
};

// Tool calls this device can see, cycled across each local session's busy time.
const TOOLS = {
  "sly-mole": [["Read", "internal/web/static/assets/style.css"], ["Edit", "internal/web/static/assets/style.css"], ["Bash", "npx playwright test sidebar"], ["Read", "internal/web/static/index.html"], ["Edit", "internal/web/static/assets/style.css"], ["Bash", "git commit"]],
  "quiet-otter": [["Read", "internal/web/static/assets/app.js"], ["Edit", "internal/web/static/assets/app.js"], ["Bash", "node --check internal/web/static/assets/app.js"], ["Bash", "go test ./internal/web"], ["Edit", "internal/web/static/assets/style.css"]],
  "brave-heron": [["Read", "internal/daemon/claims.go"], ["Edit", "internal/daemon/serve.go"], ["Bash", "go build ./..."], ["Edit", "internal/mcpserver/mcpserver.go"], ["Bash", "go test ./internal/daemon"], ["Read", "internal/proto/proto.go"]],
};

function rng(seed) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const sha = (t) => {
  let x = 2166136261;
  for (const c of t) x = Math.imul(x ^ c.charCodeAt(0), 16777619);
  return ((x >>> 0).toString(16) + "0000000").slice(0, 7);
};

export function makeBusy(demo, swarm) {
  const now = Date.now();
  const iso = (minAgo) => new Date(now - minAgo * MIN).toISOString();
  const agoOf = (s) => (now - Date.parse(s)) / MIN;
  const rand = rng(183);

  // Sessions and the new device.
  const devices = demo.state.devices;
  if (!devices.some((d) => d.id === "jun-desktop-5e19")) devices.splice(3, 0, { id: "jun-desktop-5e19", owner: "jun", reachable: true, lastSeen: iso(0.4), sessions: [] });
  for (const [dev, name, agent, branch, doing, started, active] of NEW) {
    const d = devices.find((x) => x.id === dev);
    d.sessions.push({ name, agent, branch, doing, started: iso(started), lastActive: iso(active), addr: dev + "/" + sha(name).slice(0, 4), tags: [] });
  }
  const sess = new Map();
  for (const d of devices) for (const s of d.sessions) sess.set(s.name, { d, s, start: agoOf(s.started), off: d.reachable ? 0 : agoOf(d.lastSeen) });
  const addr = (n) => sess.get(n)?.s.addr || "";

  // Messages.
  const out = [];
  let id = 0;
  const add = (ago, from, to, toKind, subject, body, re) => {
    const m = { id: "busy-" + id++, at: iso(ago), from, fromAddr: addr(from), to, toAddr: toKind === "session" ? addr(to) : "", toKind, subject, body };
    if (re) m.re = re;
    out.push(m);
    return m;
  };
  const reads = demo.history.reads;
  for (const [a, b, list] of TALK) {
    const A = sess.get(a), B = sess.get(b);
    const lo = Math.min(A.start, B.start) - 3;
    const hi = Math.max(A.off, B.off) + 1.5;
    const n = list.length;
    list.forEach(([subject, q, ans], i) => {
      const [asker, answerer] = i % 2 ? [b, a] : [a, b];
      const t = lo - ((i + 0.25 + rand() * 0.5) / n) * (lo - hi);
      const ask = add(t, asker, answerer, "session", subject, q);
      const lag = 0.6 + rand() * 3.5;
      reads[ask.id] = { at: iso(Math.max(0.1, t - lag * 0.4)), how: "turn" };
      if (ans) {
        // Answers come before either side's device went offline.
        const at = Math.max(0.15, hi - 1.4, t - lag);
        const r = add(at, answerer, asker, "session", "Re: " + subject, ans, ask.id);
        reads[r.id] = { at: iso(Math.max(0.1, at - 0.3)), how: "turn" };
      }
    });
  }
  // amber-finch hasn't read hazy-condor's question.
  for (const [ago, from, to, toKind, subject, body] of FIXED) add(ago, from, to, toKind, subject, body);
  demo.messages.push(...out);

  // History: who started when, what they said they're doing, their commits, and local tool calls.
  const ev = demo.history.events;
  for (const [, name, , branch, doing, started] of NEW) {
    ev.push({ at: iso(started), who: name, kind: "started" });
    ev.push({ at: iso(started - 1.5), who: name, kind: "status", doing });
    const list = COMMITS[name] || [];
    const local = sess.get(name).d.self;
    list.forEach((subject, i) => {
      const t = started - 6 - ((i + 0.3 + rand() * 0.4) / list.length) * (started - 8);
      ev.push({ at: iso(t), who: name, kind: "commit", branch, subject, sha: sha(name + subject), pushed: !(local && i === list.length - 1) });
    });
  }
  // Tool calls fill each local session's time up to its earliest one already in the history.
  for (const [name, cycle] of Object.entries(TOOLS)) {
    const S = sess.get(name);
    const known = ev.filter((e) => e.who === name && e.kind === "tool").map((e) => agoOf(e.at));
    const until = known.length ? Math.max(...known) + 0.5 : agoOf(S.s.lastActive);
    let t = S.start - 2;
    let i = 0;
    while (t > until) {
      const [tool, detail] = cycle[i++ % cycle.length];
      ev.push({ at: iso(t), who: name, kind: "tool", tool, detail, ok: true });
      t -= 1.2 + rand() * 2.6;
    }
  }
  ev.sort((x, y) => Date.parse(x.at) - Date.parse(y.at));

  // The swarm's view of the work.
  const web = swarm.streams.find((s) => s.id === "web-page");
  web.who.push("sly-mole", "vivid-marmot", "proud-walrus");
  web.done.push("Search scans messages on the daemon");
  web.now = "quiet-otter builds the settings view, sly-mole the phone layout, and vivid-marmot and proud-walrus add search.";
  swarm.streams.push({
    id: "windows", phase: "working", title: "Windows support", issue: 44, branch: "windows", who: ["copper-wasp", "lone-gannet", "ivory-crab"], tone: "ok",
    goal: "Let teammates on Windows run Murmur: mur.exe, the daemon, the hook and the docs.",
    done: ["Hook paths with spaces quoted", "Tests green on windows-latest", "Install page drafted"],
    now: "Starting the daemon without sh, and asking around for a code-signing certificate.",
    next: "Ship a zip for the first Windows release.",
  });
  swarm.unknown.push({ who: "hazy-condor", note: "Writing the join doc on docs-join. No issue or PR, so it's in no stream, yet it has asked six agents about their work." });
  const area = (path) => {
    let a = swarm.areas.find((x) => x.path === path);
    if (!a) swarm.areas.push((a = { path, files: [] }));
    return a;
  };
  const touch = (path, file, who) => {
    const a = area(path);
    const f = a.files.find((x) => x[0] === file);
    if (f) f[1].push(...who.filter((w) => !f[1].includes(w)));
    else a.files.push([file, who]);
  };
  touch("internal/web", "static/assets/app.js", ["vivid-marmot"]);
  touch("internal/web", "static/assets/style.css", ["sly-mole"]);
  touch("internal/web", "search.go", ["proud-walrus"]);
  touch("internal/proto", "proto.go", ["proud-walrus"]);
  touch("internal/hook", "hook.go", ["copper-wasp"]);
  touch("internal/daemon", "listen_windows.go", ["lone-gannet"]);
  touch("internal/daemon", "restart_test.go", ["lone-gannet"]);
  touch("cmd/mur", "main.go", ["copper-wasp"]);
  touch("cmd/mur", "start_windows.go", ["copper-wasp"]);
  touch(".github/workflows", "test.yml", ["lone-gannet"]);
  touch("docs", "agents/join.md", ["hazy-condor"]);
  touch("docs", "agents/use.md", ["hazy-condor", "ivory-crab"]);
  touch("docs", "install-windows.md", ["ivory-crab"]);
}
