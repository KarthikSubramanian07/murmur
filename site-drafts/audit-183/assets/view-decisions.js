// #183 view: Decisions. What the agents settled among themselves, and what they haven't yet, as
// plain sentences with the messages behind each one a click away. Ideas taken from:
//   - Architecture decision records: one sentence per decision, who made it, when, and why.
//   - Linear's and GitHub's lists: one row per item, quiet columns for when and where.
//   - Loomio's proposals: an open question shows each side in its own words.
// Grouped by time, not by stream: at 16 agents most streams settle one to three things each, so
// stream headings would be nearly as many as the rows, and the decisions that matter most (the ones
// that reach outside their stream) belong to no single stream anyway. Each row names its stream.
window.MURVIEWS ||= {};

// ---------------------------------------------------------------------------------------------
// In the product a small model reads the bus and writes these, the way it writes swarm.topics.
// Here they are written by hand from the messages in demo.js and busy.js. An entry shows only when
// every id in its msgs exists, so the small demo shows fewer. An `also` block applies when its own
// msgs exist too (the busy demo adds agents who joined the same decision later).
const DC_RAW = [
  // Still open.
  {
    id: "word-lists-final", open: true, stream: "session-names",
    what: "Whether the word lists for session names are final.",
    who: ["quiet-otter", "velvet-moth"],
    msgs: ["demo-11", "demo-21"],
    sides: [
      { who: "quiet-otter", says: "Asked, because the page shows names everywhere and needs to know if they can change." },
      { who: "velvet-moth", says: "Said both lists are in, 412 adjectives and 389 nouns, then went on do not disturb. Hasn't seen the question." },
    ],
  },
  {
    id: "testrestart-macos", open: true, stream: "ls-remote",
    what: "Whether TestRestart still fails on macOS after rapid-ibis changed restart_test.go.",
    who: ["rapid-ibis", "dusty-lynx", "brave-heron"],
    msgs: ["demo-5", "demo-6", "demo-20"],
    sides: [
      { who: "rapid-ibis", says: "Needs a rerun on the Mac mini before its ls-remote change can go in." },
      { who: "dusty-lynx", says: "Reported it failing on macOS only. Its Mac mini went offline before the rerun was asked for." },
      { who: "brave-heron", says: "Will take the fix once #24 merges." },
      { who: "lone-gannet", says: "Skipped it on Windows for now, so the Windows run is green.", need: "busy-48" },
    ],
    also: { msgs: ["busy-47", "busy-48"], who: ["rapid-ibis", "dusty-lynx", "brave-heron", "lone-gannet"] },
  },
  {
    id: "sign-mur-exe", open: true, stream: "windows",
    what: "Who can sign mur.exe, which Windows Defender flags the first time it runs.",
    who: ["copper-wasp", "lone-gannet", "ivory-crab"],
    msgs: ["busy-40", "busy-55", "busy-51"],
    sides: [
      { who: "copper-wasp", says: "Asked whether anyone has a code-signing certificate. The zip was chosen over an MSI for the same reason." },
      { who: "lone-gannet", says: "Read the question and hasn't answered." },
      { who: "ivory-crab", says: "The install page already says what to do when Defender blocks it." },
    ],
    needs: "Needs a person: an agent can't buy a code-signing certificate.",
  },
  {
    id: "amber-finch-main", open: true, stream: null,
    what: "What amber-finch is doing on main, with no status set.",
    who: ["lucid-badger", "hazy-condor", "amber-finch"],
    msgs: ["demo-19", "busy-109"],
    sides: [
      { who: "lucid-badger", says: "Asked whether it touches refs/murmur/names, which lucid-badger was about to change." },
      { who: "hazy-condor", says: "Asked whether it writes docs, so the two don't clash." },
      { who: "amber-finch", says: "Read lucid-badger's question from its inbox, hasn't opened hazy-condor's, and answered neither." },
    ],
  },

  // Decided.
  {
    id: "remove-claims", stream: "remove-claims",
    what: "Claims are going: #24 deletes claims.go and the claim and release tools, and wake-ups reads nothing from them.",
    who: ["brave-heron", "solar-wren"],
    msgs: ["demo-2", "demo-3"],
    affects: "anything that reads claims or calls the claim and release tools",
    also: {
      what: "Claims are going: #24 deletes claims.go and the claim and release tools, and neither wake-ups nor names reads them.",
      who: ["brave-heron", "solar-wren", "lucid-badger"],
      msgs: ["busy-100", "busy-101"],
    },
  },
  {
    id: "names-are-addresses", stream: "session-names",
    what: "Agents send to two-word names like quiet-otter, kept on refs/murmur/names, instead of device/session addresses.",
    who: ["lucid-badger"],
    msgs: ["demo-4"],
    affects: "every agent that sends a message, and anything that stores an address",
    also: {
      what: "Agents send to two-word names like quiet-otter instead of device/session addresses. The join doc says so, and that device/session works until a name lands.",
      who: ["lucid-badger", "hazy-condor"],
      msgs: ["busy-78", "busy-79"],
    },
  },
  {
    id: "names-before-landing", stream: "session-names",
    what: "A session appears only once its name lands, and the page looks the name up on every poll, because a name changes once if its first push fails.",
    who: ["quiet-otter", "lucid-badger"],
    msgs: ["demo-7", "demo-8", "demo-9", "demo-10"],
    also: { who: ["quiet-otter", "lucid-badger", "hazy-condor"], msgs: ["busy-82", "busy-83"] },
  },
  {
    id: "poll-10s", stream: "ls-remote",
    what: "Every device keeps polling GitHub every 10 seconds: 4 devices for 30 minutes hit no rate limit.",
    who: ["rapid-ibis"],
    msgs: ["demo-12"],
    also: {
      what: "Every device keeps polling GitHub every 10 seconds, Windows included: 4 devices for 30 minutes hit no rate limit.",
      who: ["rapid-ibis", "lone-gannet"],
      msgs: ["busy-74", "busy-75"],
    },
  },
  {
    id: "sessionview-shape", stream: "remove-claims",
    what: "Only Claims leaves proto.SessionView. Doing and every other field stay.",
    who: ["brave-heron", "quiet-otter"],
    msgs: ["demo-13", "demo-14", "demo-15", "demo-16"],
    affects: "anything that reads proto.SessionView",
    also: { who: ["brave-heron", "quiet-otter", "proud-walrus"], msgs: ["busy-35", "busy-36"], affects: "the page and search, which both read proto.SessionView" },
  },
  {
    id: "idle-time", stream: "wake-ups",
    what: "The daemon keeps when each session's last turn ended, and wake-ups hands that idle time to read receipts.",
    who: ["mellow-yak", "solar-wren"],
    msgs: ["demo-17", "demo-18"],
  },
  // Only in the busy demo.
  {
    id: "merge-order", stream: "remove-claims",
    what: "#31 (names) merges first, then #24 (claims), and search rebases after both, because all three change proto.go.",
    who: ["brave-heron", "lucid-badger", "proud-walrus"],
    msgs: ["busy-98", "busy-99", "busy-37", "busy-38"],
    affects: "whoever merges #31, #24 and the search branch",
  },
  {
    id: "stop-hook", stream: "wake-ups",
    what: "The Stop hook calls mur directly instead of sh -c, so wake-ups work on Windows. copper-wasp keeps to the path quoting at the top of hook.go.",
    who: ["copper-wasp", "solar-wren"],
    msgs: ["busy-64", "busy-65", "busy-66", "busy-67"],
    affects: "every agent's Stop hook",
  },
  {
    id: "search-on-daemon", stream: "web-page",
    what: "Search scans messages on the daemon with no index (10,000 take 4 ms), served as GET /api/search with match ranges.",
    who: ["vivid-marmot", "proud-walrus"],
    msgs: ["busy-19", "busy-20", "busy-21", "busy-22", "busy-27", "busy-28"],
  },
  {
    id: "windows-daemon", stream: "windows",
    what: "On Windows the daemon is a process mur starts on first use, listening on AF_UNIX, because everyone they know runs Windows 11.",
    who: ["lone-gannet", "copper-wasp"],
    msgs: ["busy-41", "busy-42", "busy-49", "busy-50"],
  },
  {
    id: "windows-zip", stream: "windows",
    what: "The first Windows release ships as a zip with mur.exe, not an MSI, because an MSI needs signing nobody has.",
    who: ["copper-wasp", "lone-gannet"],
    msgs: ["busy-39", "busy-40"],
  },
];
// ---------------------------------------------------------------------------------------------

window.MURDECISIONS = (mur) => {
  const byId = new Map(mur.messages.map((m) => [m.id, m]));
  const out = [];
  for (const raw of DC_RAW) {
    if (!raw.msgs.every((id) => byId.has(id))) continue;
    const also = raw.also && raw.also.msgs.every((id) => byId.has(id)) ? raw.also : null;
    const ids = [...new Set([...raw.msgs, ...(also ? also.msgs : [])])].sort((a, b) => byId.get(a).sortAt - byId.get(b).sortAt);
    const e = {
      id: raw.id,
      at: byId.get(ids[ids.length - 1]).sortAt,
      what: also?.what || raw.what,
      who: also?.who || raw.who,
      stream: raw.stream,
      msgs: ids,
      open: !!raw.open,
    };
    const affects = also?.affects || raw.affects;
    if (affects) e.affects = affects;
    if (raw.sides) e.sides = raw.sides.filter((s) => !s.need || byId.has(s.need)).map(({ who, says }) => ({ who, says }));
    if (raw.needs) e.needs = raw.needs;
    out.push(e);
  }
  return out.sort((a, b) => b.at - a.at);
};

window.MURVIEWS.decisions = (mur) => {
  const { h, ui } = mur;
  ui.dcMarks ||= {};
  ui.dcOpen ||= {};
  const all = window.MURDECISIONS(mur);
  const open = all.filter((d) => d.open);
  const done = all.filter((d) => !d.open);
  const reach = done.filter((d) => d.affects);
  const show = ui.dcShow === "reach" ? reach : done;

  const now = Date.now();
  const HOUR = 3600e3;
  const oldest = done.length ? Math.min(...done.map((d) => d.at)) : now;
  const hours = Math.max(1, Math.ceil((now - oldest) / HOUR));

  const recent = show.filter((d) => now - d.at < HOUR);
  const earlier = show.filter((d) => now - d.at >= HOUR);

  // What the list was read from, so a person knows what they can skip.
  const byId = new Map(mur.messages.map((m) => [m.id, m]));
  const cited = new Set(all.flatMap((d) => d.msgs));
  const from = Math.min(...all.flatMap((d) => d.msgs.map((id) => byId.get(id).sortAt)), now);
  const inWindow = mur.messages.filter((m) => m.sortAt >= from);
  const rest = inWindow.filter((m) => !cited.has(m.id)).length;

  const marked = done.filter((d) => ui.dcMarks[d.id]).length;

  return h(
    "div",
    { class: "fx dc" },
    h(
      "header",
      { class: "fx-head" },
      h(
        "div",
        null,
        h("h1", null, "Decisions"),
        h("p", null, mur.plural(done.length, "decision") + " in the last " + mur.plural(hours, "hour") + ", " + open.length + " still open, " + reach.length + " that reach outside their stream."),
      ),
    ),

    open.length > 0 &&
      h(
        "section",
        { class: "dc-section" },
        h("header", { class: "dc-section-head" }, h("h2", null, "Still open"), h("span", { class: "dc-count" }, open.length)),
        h("div", { class: "dc-open-list" }, open.map((d) => dcOpenCard(mur, d))),
      ),

    h(
      "section",
      { class: "dc-section" },
      h(
        "header",
        { class: "dc-section-head" },
        h("h2", null, "Decided"),
        h("span", { class: "dc-count" }, done.length),
        h("span", { class: "fx-spacer" }),
        mur.fxSeg(
          "Which decisions",
          [
            ["all", "All", done.length],
            ["reach", "Reach outside their stream", reach.length],
          ],
          ui.dcShow === "reach" ? "reach" : "all",
          (v) => {
            ui.dcShow = v;
            mur.render();
          },
        ),
      ),
      recent.length > 0 && [h("h3", { class: "dc-when" }, "In the last hour"), h("ol", { class: "dc-list" }, recent.map((d) => dcRow(mur, d)))],
      earlier.length > 0 && [h("h3", { class: "dc-when" }, "Earlier"), h("ol", { class: "dc-list" }, earlier.map((d) => dcRow(mur, d)))],
      show.length === 0 && h("p", { class: "dc-empty" }, "Nothing decided here reaches outside its stream."),
    ),

    h(
      "footer",
      { class: "dc-foot" },
      h(
        "p",
        null,
        "Written from " + cited.size + " of the " + inWindow.length + " messages sent in the last " + mur.plural(Math.max(1, Math.ceil((now - from) / HOUR)), "hour") + ". The other " + rest + " are routine.",
        marked > 0 && " You've marked " + marked + " of " + done.length + ".",
      ),
      h("button", { class: "dc-link", onclick: () => mur.go("messages") }, "All conversations", mur.icon("arrowRight")),
    ),
  );
};

function dcStreamTitle(mur, id) {
  if (!id) return "No stream";
  return mur.swarm.streams.find((s) => s.id === id)?.title || id;
}

function dcNames(h, names) {
  return names.map((n, i) => [i ? (i === names.length - 1 ? " and " : ", ") : "", h("b", null, n)]);
}

// Who settled it: "lucid-badger, to everyone" for a broadcast nobody answered, otherwise the names.
function dcWho(mur, d) {
  const { h } = mur;
  const first = mur.messages.find((m) => m.id === d.msgs[0]);
  const announced = d.who.length === 1 && first?.toKind === "all";
  return h("span", { class: "dc-who" }, mur.briefFaces(d.who), h("span", { class: "dc-who-names" }, dcNames(h, d.who), announced ? ", to everyone" : ""));
}

function dcEvidenceToggle(mur, d) {
  const { h, ui } = mur;
  const isOpen = !!ui.dcOpen[d.id];
  return h(
    "button",
    {
      class: "dc-link" + (isOpen ? " on" : ""),
      "aria-expanded": isOpen ? "true" : "false",
      onclick: () => {
        ui.dcOpen[d.id] = !isOpen;
        mur.render();
      },
    },
    mur.icon("message"),
    mur.plural(d.msgs.length, "message"),
    mur.icon(isOpen ? "chevronDown" : "chevronRight"),
  );
}

// The messages themselves, in order. Each opens its conversation.
function dcEvidence(mur, d) {
  const { h, ui } = mur;
  if (!ui.dcOpen[d.id]) return null;
  const byId = new Map(mur.messages.map((m) => [m.id, m]));
  return h(
    "ol",
    { class: "dc-evidence" },
    d.msgs.map((id) => {
      const m = byId.get(id);
      const to = m.toKind === "all" ? "everyone" : m.toKind === "device" ? "every session on " + m.to.replace(/-[0-9a-f]{4}$/, "") : mur.toName(m);
      return h(
        "li",
        null,
        h(
          "button",
          { class: "dc-quote", onclick: () => mur.swOpenMessage(m), title: "Open this conversation" },
          mur.avatar(mur.fromName(m)),
          h(
            "span",
            { class: "dc-quote-text" },
            h("span", { class: "dc-quote-head" }, h("b", null, mur.fromName(m)), " to " + to + " · ", h("span", { class: "dc-quote-ago" }, mur.ago(m.sortAt))),
            h("span", { class: "dc-quote-body" }, m.body.replace(/`/g, "").replace(/\s+/g, " ")),
          ),
        ),
      );
    }),
  );
}

function dcOpenCard(mur, d) {
  const { h } = mur;
  return h(
    "article",
    { class: "dc-open" },
    h("p", { class: "dc-q" }, d.what),
    h(
      "ul",
      { class: "dc-sides" },
      d.sides.map((s) => h("li", null, mur.avatar(s.who), h("p", null, h("b", null, s.who), " ", s.says))),
    ),
    d.needs && h("p", { class: "dc-needs" }, mur.icon("users"), h("span", null, d.needs)),
    h(
      "div",
      { class: "dc-meta" },
      mur.agoEl("span", "dc-ago", d.at, "Last word "),
      h("span", { class: "dc-dot", "aria-hidden": "true" }),
      h("span", { class: "dc-stream" }, dcStreamTitle(mur, d.stream)),
      h("span", { class: "fx-spacer" }),
      dcEvidenceToggle(mur, d),
    ),
    dcEvidence(mur, d),
  );
}

function dcRow(mur, d) {
  const { h, ui } = mur;
  const mark = ui.dcMarks[d.id] || null;
  const setMark = (v) => () => {
    if (ui.dcMarks[d.id] === v) delete ui.dcMarks[d.id];
    else ui.dcMarks[d.id] = v;
    mur.render();
  };
  return h(
    "li",
    { class: "dc-row" + (mark ? " marked-" + mark : "") + (d.affects ? " reaches" : "") },
    mur.agoEl("span", "dc-ago dc-row-ago", d.at),
    h(
      "div",
      { class: "dc-body" },
      h("p", { class: "dc-what" }, d.what),
      d.affects && h("p", { class: "dc-affects" }, h("span", { class: "dc-affects-dot", "aria-hidden": "true" }), h("span", null, "Affects " + d.affects)),
      h("div", { class: "dc-meta" }, dcWho(mur, d), dcEvidenceToggle(mur, d), h("span", { class: "dc-stream dc-stream-inline" }, dcStreamTitle(mur, d.stream))),
      mark === "question" && h("p", { class: "dc-questioned" }, "You questioned this."),
      dcEvidence(mur, d),
    ),
    h("span", { class: "dc-stream dc-stream-col" }, dcStreamTitle(mur, d.stream)),
    h(
      "div",
      { class: "dc-marks", role: "group", "aria-label": "Your mark" },
      h("button", { class: "dc-mark" + (mark === "fine" ? " on fine" : ""), "aria-pressed": mark === "fine" ? "true" : "false", onclick: setMark("fine") }, mur.icon("check"), "Fine"),
      h("button", { class: "dc-mark" + (mark === "question" ? " on question" : ""), "aria-pressed": mark === "question" ? "true" : "false", onclick: setMark("question") }, mur.icon("reply"), "Question"),
    ),
  );
}

window.MUR?.render?.();
