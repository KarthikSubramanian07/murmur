// #183 view: Board. The swarm's work as cards moving across columns, so a person sees the shape of
// the work before the agents doing it. Borrowed from Vibe Kanban (To do / In progress / In review /
// Done, with agents on each card), Claude Code's agent teams (one task list everyone can see),
// ParallelPilot's Done / Running / Next, and Linear's project cards.
window.MURVIEWS ||= {};

const COLUMNS = [
  ["backlog", "Not started", "Issues nobody is working on"],
  ["working", "Being worked on", "An agent is on it now"],
  ["waiting", "Waiting", "Held up on another agent, a device or a person"],
  ["review", "Ready for a person", "The agents think it's done; a PR is open"],
  ["done", "Merged today", "Finished in the last day"],
];

window.MURVIEWS.board = (mur) => {
  const { h, ui, swarm } = mur;
  const open = ui.bdOpen || null;
  const cols = {
    backlog: swarm.backlog.map((b) => ({ kind: "issue", ...b })),
    working: swarm.streams.filter((s) => s.phase === "working").map((s) => ({ kind: "stream", s })),
    waiting: swarm.streams.filter((s) => s.phase === "waiting").map((s) => ({ kind: "stream", s })),
    review: swarm.streams.filter((s) => s.phase === "review").map((s) => ({ kind: "stream", s })),
    done: swarm.done.map((d) => ({ kind: "done", ...d })),
  };
  const agents = swarm.streams.reduce((n, s) => n + s.who.length, 0);
  return h(
    "div",
    { class: "fx bd" },
    h(
      "header",
      { class: "fx-head" },
      h(
        "div",
        null,
        h("h1", null, "Board"),
        h(
          "p",
          null,
          plural(swarm.streams.length, "piece") + " of work in flight with " + agents + " agents on them, " + cols.review.length + " ready for a person to look at, and " + cols.backlog.length + " issues nobody has picked up.",
        ),
      ),
    ),
    h(
      "div",
      { class: "bd-cols" },
      COLUMNS.map(([key, title, sub]) =>
        h(
          "section",
          { class: "bd-col bd-col-" + key },
          h("header", { class: "bd-col-head" }, h("h2", null, title), h("span", { class: "fx-badge" }, cols[key].length), h("p", null, sub)),
          h("div", { class: "bd-cards" }, cols[key].length ? cols[key].map((c) => bdCard(mur, c, key, open)) : h("p", { class: "bd-empty" }, "Nothing here")),
        ),
      ),
    ),
    swarm.unknown.length > 0 &&
      h(
        "p",
        { class: "bd-foot" },
        "Not on the board: ",
        swarm.unknown.map((u) => [mur.avatar(u.who), h("b", null, u.who), " — " + u.note]),
      ),
  );
};

function plural(n, w) {
  return n + " " + w + (n === 1 ? "" : "s");
}

function bdCard(mur, c, col, open) {
  const { h, ui } = mur;
  if (c.kind === "issue")
    return h("article", { class: "bd-card quiet" }, h("div", { class: "bd-card-top" }, h("span", { class: "bd-title" }, c.title), h("span", { class: "bd-ref" }, "#" + c.issue)), h("p", { class: "bd-note" }, c.note), h("p", { class: "bd-nobody" }, "Nobody on it"));
  if (c.kind === "done")
    return h(
      "article",
      { class: "bd-card quiet" },
      h("div", { class: "bd-card-top" }, h("span", { class: "bd-title" }, c.title), h("span", { class: "bd-ref" }, "#" + c.issue)),
      h("div", { class: "bd-meta" }, mur.briefFaces(c.who), h("span", null, c.who.join(", ") + " · merged " + mur.ago(Date.now() - c.at * 60000))),
    );
  const s = c.s;
  const isOpen = open === s.id;
  const loop = mur.messages.filter((m) => s.who.includes(mur.fromName(m)) || (m.toKind === "session" && s.who.includes(mur.toName(m)))).sort((a, b) => b.sortAt - a.sortAt);
  const last = loop[0];
  const tone = { ok: "on", warn: "idle", error: "dnd", off: "off" }[s.tone] || "off";
  const lastActive = Math.max(...s.who.map((n) => mur.when(mur.findSession(n)?.session.lastActive) || 0));
  return h(
    "article",
    { class: "bd-card" + (isOpen ? " open" : "") + (s.tone === "error" ? " trouble" : "") },
    h(
      "button",
      {
        class: "bd-card-btn",
        "aria-expanded": isOpen ? "true" : "false",
        onclick: () => {
          ui.bdOpen = isOpen ? null : s.id;
          mur.render();
        },
      },
      h(
        "div",
        { class: "bd-card-top" },
        mur.fxDot(tone),
        h("span", { class: "bd-title" }, s.title),
      ),
      h("div", { class: "bd-refs" }, s.issue && h("span", { class: "bd-ref" }, "#" + s.issue), s.pr && h("span", { class: "fx-pr" + (s.draft ? " draft" : "") }, "PR #" + s.pr + (s.draft ? " · draft" : ""))),
      h("p", { class: "bd-now" }, s.now),
      col === "waiting" && s.waiting && h("p", { class: "bd-wait" }, mur.icon("clock"), h("span", null, "Waiting on ", h("b", null, s.waiting.on), " for " + s.waiting.what)),
      col === "waiting" && !s.waiting && s.tone === "off" && h("p", { class: "bd-wait" }, mur.icon("laptop"), h("span", null, "Its device is offline")),
      h(
        "div",
        { class: "bd-progress", title: s.done.length + " done, 1 in progress, then: " + s.next },
        s.done.map(() => h("i", { class: "d" })),
        h("i", { class: "n" }),
        h("i", { class: "x" }),
      ),
      h("div", { class: "bd-meta" }, mur.briefFaces(s.who), h("span", { class: "truncate" }, s.who.join(", ")), lastActive > 0 && h("span", { class: "bd-ago" }, mur.ago(lastActive))),
    ),
    isOpen &&
      h(
        "div",
        { class: "bd-more" },
        h("p", { class: "bd-goal" }, s.goal),
        h("h3", null, "Done"),
        h("ul", null, s.done.map((d) => h("li", null, d))),
        h("h3", null, "Next"),
        h("p", null, s.next),
        last &&
          h(
            "button",
            { class: "bd-last", onclick: () => mur.swOpenMessage(last) },
            h("span", { class: "bd-last-k" }, "Latest message"),
            h("span", null, h("b", null, mur.fromName(last)), " → " + (last.toKind === "all" ? "everyone" : last.toKind === "device" ? "a device" : mur.toName(last)) + ": " + last.subject),
          ),
      ),
  );
}

window.MUR?.render?.();
