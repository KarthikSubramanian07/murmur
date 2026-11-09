// #183 view: Timeline. One lane per agent across the last hours, with what it did on its lane and the
// messages between agents drawn from lane to lane. Borrowed from the swimlanes in disler's Claude Code
// multi-agent observability dashboard and Agents Observe, AgentOps' session waterfall, Temporal's
// event timeline, and sequence diagrams (a message is an arrow from sender to receiver).
window.MURVIEWS ||= {};

const WINDOWS = [
  ["1", "1 hour", 60],
  ["3", "3 hours", 180],
  ["6", "6 hours", 360],
];
const LANE = 48; // px per lane
const HEAD = 30; // px per group heading

window.MURVIEWS.timeline = (mur) => {
  const { h, ui } = mur;
  const win = WINDOWS.find((w) => w[0] === (ui.tlWin || "3")) || WINDOWS[1];
  const by = ui.tlBy || "stream";
  const end = Date.now();
  const start = end - win[2] * 60000;
  const pct = (t) => Math.max(0, Math.min(100, ((t - start) / (end - start)) * 100));
  const H = mur.history();
  const events = H ? H.events : [];

  // Lanes: every running session, grouped by stream or by device.
  const all = [];
  for (const d of mur.devices()) for (const s of d.sessions) all.push({ d, s, stream: mur.swStreamOf(s.name) });
  const groups = [];
  if (by === "stream") {
    for (const st of mur.swarm.streams) {
      const lanes = all.filter((l) => l.stream?.id === st.id);
      if (lanes.length) groups.push({ key: st.id, title: st.title, sub: st.issue ? "#" + st.issue : "", lanes });
    }
    const rest = all.filter((l) => !l.stream);
    if (rest.length) groups.push({ key: "none", title: "No stream", sub: "", lanes: rest });
  } else {
    for (const d of mur.devices()) {
      const lanes = all.filter((l) => l.d.id === d.id);
      if (lanes.length) groups.push({ key: d.id, title: d.self ? "This device" : (d.owner ? d.owner + "'s " : "") + d.id.replace(/-[0-9a-f]{4}$/, ""), sub: d.reachable ? "" : "offline", lanes });
    }
  }
  // y position of each lane's centre, counting group headings.
  const ys = new Map();
  let y = 0;
  for (const g of groups) {
    g.y = y;
    y += HEAD;
    for (const l of g.lanes) {
      ys.set(l.s.name, y + LANE / 2);
      y += LANE;
    }
  }
  const height = y;

  const sel = ui.tlSel || null;
  const pick = (v) => () => {
    ui.tlSel = sel && sel.id === v.id ? null : v;
    mur.render();
  };

  // Time axis ticks.
  const step = win[2] <= 60 ? 10 : win[2] <= 180 ? 30 : 60;
  const ticks = [];
  const first = Math.ceil(start / (step * 60000)) * step * 60000;
  for (let t = first; t <= end; t += step * 60000) ticks.push(t);
  const T = (t) => new Date(t).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });

  const plot = [];
  // Lane rails: active from its start to its last activity, quiet after, offline grey after the
  // device was last seen.
  for (const g of groups) {
    plot.push(h("div", { class: "tl-ghead-band", style: `top:${g.y}px;height:${HEAD}px` }));
    for (const l of g.lanes) {
      const cy = ys.get(l.s.name);
      const born = mur.when(l.s.started) || start;
      const last = mur.when(l.s.lastActive) || born;
      const seen = l.d.reachable ? end : mur.when(l.d.lastSeen) || end;
      const dnd = events.find((e) => e.who === l.s.name && e.kind === "tag" && e.tag === "do-not-disturb");
      const seg = (a, b, cls, title) => b > start && b > a && h("div", { class: "tl-rail " + cls, style: `top:${cy}px;left:${pct(a)}%;width:${pct(b) - pct(a)}%`, title });
      plot.push(h("div", { class: "tl-lane-bg", style: `top:${cy - LANE / 2}px;height:${LANE}px` }));
      plot.push(seg(born, Math.min(last, seen), "busy", l.s.name + " working"));
      if (last < seen) plot.push(seg(Math.max(born, last), seen, "quiet", l.s.name + " quiet since " + T(last)));
      if (!l.d.reachable) plot.push(seg(seen, end, "off", "Its device has been unreachable since " + T(seen)));
      if (dnd) plot.push(seg(mur.when(dnd.at), end, "dnd", "On do not disturb since " + T(mur.when(dnd.at))));
    }
  }
  // What each agent did: started, status, commits, tool runs (this device only), going offline.
  const toolRuns = new Map();
  for (const e of events) {
    if (!ys.has(e.who)) continue;
    const t = mur.when(e.at);
    if (t < start) continue;
    const cy = ys.get(e.who);
    const f = mur.findSession(e.who);
    if (e.kind === "tool") {
      if (!f?.device.self) continue; // tool calls stay on their own device
      plot.push(h("span", { class: "tl-tool" + (e.ok ? "" : " bad"), style: `top:${cy}px;left:${pct(t)}%`, title: e.tool + ": " + e.detail + (e.ok ? "" : " (failed)") }));
      continue;
    }
    const id = e.who + "@" + e.at + e.kind;
    const label =
      e.kind === "commit" ? (e.subject || "A commit not pushed yet") : e.kind === "status" ? (e.doing ? "Status: " + e.doing : "Cleared its status") : e.kind === "started" ? "Started" : e.kind === "tag" ? "Tagged " + e.tag : e.kind === "offline" ? "Device stopped answering" : e.kind;
    plot.push(
      h(
        "button",
        { class: "tl-mark tl-" + e.kind + (sel?.id === id ? " on" : ""), style: `top:${cy}px;left:${pct(t)}%`, title: T(t) + " · " + label, "aria-label": label, onclick: pick({ id, type: "event", e, t, label }) },
      ),
    );
  }
  // Messages: an arrow from the sender's lane to the receiver's lane at the time it was sent.
  // Broadcasts and messages to a device: a faint line across every lane they reached.
  for (const m of mur.messages) {
    const t = m.sortAt;
    if (t < start) continue;
    const from = mur.fromName(m);
    const fy = ys.get(from);
    const id = "m:" + m.id;
    const on = sel?.id === id;
    if (m.toKind === "session") {
      const to = mur.toName(m);
      const ty = ys.get(to);
      if (fy == null && ty == null) continue;
      if (fy == null || ty == null) {
        // one end is a settled session: a short stub
        const cy = fy ?? ty;
        plot.push(h("button", { class: "tl-stub" + (on ? " on" : ""), style: `top:${cy}px;left:${pct(t)}%`, title: T(t) + " · " + from + " → " + to + ": " + m.subject, onclick: pick({ id, type: "message", m, t }) }));
        continue;
      }
      const cross = mur.swStreamOf(from)?.id !== mur.swStreamOf(to)?.id;
      const down = ty > fy;
      plot.push(
        h(
          "button",
          {
            class: "tl-msg" + (cross ? " cross" : "") + (down ? " down" : " up") + (on ? " on" : ""),
            style: `top:${Math.min(fy, ty)}px;height:${Math.abs(ty - fy)}px;left:${pct(t)}%`,
            title: T(t) + " · " + from + " → " + to + ": " + m.subject,
            onclick: pick({ id, type: "message", m, t }),
          },
          h("span", { class: "tl-msg-dot" }),
          h("span", { class: "tl-msg-head" }),
        ),
      );
    } else if (fy != null) {
      plot.push(
        h("button", { class: "tl-cast" + (on ? " on" : ""), style: `top:0;height:${height}px;left:${pct(t)}%`, title: T(t) + " · " + from + " → " + (m.toKind === "all" ? "everyone" : m.to) + ": " + m.subject, onclick: pick({ id, type: "message", m, t }) }, h("span", { class: "tl-cast-dot", style: `top:${fy}px` })),
      );
    }
  }

  const labels = groups.map((g) => [
    h("div", { class: "tl-ghead", style: `height:${HEAD}px` }, h("span", { class: "tl-gtitle" }, g.title), g.sub && h("span", { class: "tl-gsub" }, g.sub)),
    g.lanes.map((l) => {
      const st = mur.sessionState(l.d, l.s);
      return h(
        "div",
        { class: "tl-label", style: `height:${LANE}px` },
        mur.presence(l.s.name, l.d),
        h("span", { class: "tl-label-text" }, h("span", { class: "tl-name" }, l.s.name), h("span", { class: "tl-agent" }, st === "off" ? "offline" : st === "dnd" ? "do not disturb" : st === "idle" ? "quiet " + mur.ago(mur.when(l.s.lastActive)).replace(" ago", "") : "active")),
      );
    }),
  ]);

  return h(
    "div",
    { class: "fx tl" },
    h(
      "header",
      { class: "fx-head" },
      h("div", null, h("h1", null, "Timeline"), h("p", null, "What each agent did over the last " + win[1] + ", and every message between them, drawn from sender to receiver.")),
      h(
        "div",
        { class: "tl-controls" },
        mur.fxSeg("Group lanes by", [["stream", "By stream"], ["device", "By device"]], by, (v) => {
          ui.tlBy = v;
          mur.render();
        }),
        mur.fxSeg("Window", WINDOWS.map(([k, l]) => [k, l]), win[0], (v) => {
          ui.tlWin = v;
          mur.render();
        }),
      ),
    ),
    h(
      "div",
      { class: "tl-frame" },
      h("div", { class: "tl-labels" }, h("div", { class: "tl-axis-gap" }), labels),
      h(
        "div",
        { class: "tl-scroll" },
        h(
          "div",
          { class: "tl-canvas" },
          // A label too near either end would be clipped or collide with "now"; its grid line stays.
          h("div", { class: "tl-axis" }, ticks.filter((t) => pct(t) > 4 && pct(t) < 94).map((t) => h("span", { class: "tl-tick", style: `left:${pct(t)}%` }, T(t))), h("span", { class: "tl-now-label" }, "now")),
          h("div", { class: "tl-plot", style: `height:${height}px` }, ticks.map((t) => h("span", { class: "tl-grid", style: `left:${pct(t)}%` })), plot, h("span", { class: "tl-now" })),
        ),
      ),
    ),
    h(
      "div",
      { class: "tl-legend" },
      h("span", null, h("i", { class: "lg-rail" }), "working"),
      h("span", null, h("i", { class: "lg-quiet" }), "quiet"),
      h("span", null, h("i", { class: "lg-off" }), "offline"),
      h("span", null, h("i", { class: "lg-commit" }), "commit"),
      h("span", null, h("i", { class: "lg-status" }), "status change"),
      h("span", null, h("i", { class: "lg-tool" }), "tool call (this device only)"),
      h("span", null, h("i", { class: "lg-msg" }), "message"),
      h("span", null, h("i", { class: "lg-cross" }), "message between streams"),
      h("span", null, h("i", { class: "lg-cast" }), "to everyone"),
    ),
    tlDetail(mur, sel),
  );
};

function tlDetail(mur, sel) {
  const { h } = mur;
  if (!sel) return h("p", { class: "tl-hint" }, "Click a message or a mark on a lane to read it.");
  const T = (t) => new Date(t).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  if (sel.type === "message") {
    const m = mur.messages.find((x) => x.id === sel.m.id) || sel.m;
    const to = m.toKind === "all" ? "everyone" : m.toKind === "device" ? m.to : mur.toName(m);
    const read = mur.history()?.reads[m.id];
    return h(
      "section",
      { class: "tl-detail" },
      h("div", { class: "tl-detail-head" }, mur.avatar(mur.fromName(m)), h("b", null, mur.fromName(m)), h("span", { class: "fx-faint" }, " → " + to + " · " + T(sel.t)), read && h("span", { class: "fx-faint" }, " · read " + Math.max(1, Math.round((mur.when(read.at) - mur.when(m.at)) / 60000)) + "m later")),
      h("p", { class: "tl-detail-subj" }, m.subject),
      m.body && h("p", { class: "tl-detail-body" }, m.body),
      h("button", { class: "fx-btn", onclick: () => mur.swOpenMessage(m) }, mur.icon("message"), "Open the conversation"),
    );
  }
  const e = sel.e;
  return h(
    "section",
    { class: "tl-detail" },
    h("div", { class: "tl-detail-head" }, mur.avatar(e.who), h("b", null, e.who), h("span", { class: "fx-faint" }, " · " + T(sel.t))),
    h("p", { class: "tl-detail-subj" }, sel.label),
    e.kind === "commit" && h("p", { class: "fx-faint" }, h("span", { class: "mono" }, e.sha), " on ", h("span", { class: "mono" }, e.branch), e.pushed ? "" : " · not pushed"),
  );
}

window.MUR?.render?.();
