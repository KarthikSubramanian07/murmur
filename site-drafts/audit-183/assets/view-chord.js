// #183 view: "Between streams". Which pieces of work are entangled with which, and through which
// agents. Three forms of the same messages, at the stream level and the agent level:
//   - Streams: a chord diagram (d3.chord, d3.ribbon), after Mike Bostock's d3 chord diagrams and
//     the Circos tradition they come from. One arc per stream, one ribbon per pair of streams.
//   - Agents: hierarchical edge bundling (Danny Holten, 2006), after Bostock's d3 example:
//     agents on a ring grouped by stream, every pair that messaged joined by a bundled curve.
//   - Matrix: an adjacency matrix, after Bostock's "Les Misérables co-occurrence", because
//     Ghoniem, Fekete and Castagliola (2004) found matrices read better than node-link drawings
//     once a graph gets dense.
// Only running sessions are drawn. Messages with sessions that have stopped, broadcasts and
// messages to a whole device are left out, and the reading says how many.
const SVGNS = "http://www.w3.org/2000/svg";
const WINDOWS = [["1h", "Last hour"], ["3h", "Last 3 hours"], ["today", "Today"], ["all", "Everything"]];
const FORMS = [["streams", "Streams"], ["agents", "Agents"], ["matrix", "Matrix"]];
const NONE = "_none";
const STATE_TEXT = { on: "Active", idle: "Idle", dnd: "Do not disturb", off: "Offline" };
// The palette's blue ramp, dark to light: on the dark page, more messages is lighter.
const RAMP = ["#184f95", "#1c5cab", "#256abf", "#2a78d6", "#3987e5", "#5598e7", "#6da7ec", "#86b6ef", "#9ec5f4", "#b7d3f6", "#cde2fb"];
const SENT = "#6d9cfe";
const RECV = "#e6e6e6";

function s(tag, attrs, ...kids) {
  const el = document.createElementNS(SVGNS, tag);
  for (const [k, v] of Object.entries(attrs || {})) if (v != null && v !== false) el.setAttribute(k, v);
  for (const k of kids.flat()) if (k != null && k !== false) el.append(k);
  return el;
}
const f1 = (n) => (Math.round(n * 10) / 10).toString();

let ctx2d = null;
function textWidth(t, px, weight = 400) {
  ctx2d ||= document.createElement("canvas").getContext("2d");
  ctx2d.font = `${weight} ${px}px "Inter Variable", Inter, system-ui, sans-serif`;
  return ctx2d.measureText(t).width;
}

// Rerender when the window crosses the phone breakpoint: the forms lay out differently there.
let lastNarrow = null;
window.addEventListener("resize", () => {
  const n = window.innerWidth < 760;
  if (lastNarrow !== null && n !== lastNarrow && document.querySelector(".ch")) window.MUR?.render?.();
});

// ---------- The data: who wrote to whom, inside the window ----------
function model(mur) {
  const { devices, messages, swarm, sessionState, fromName, toName, when } = mur;
  const ui = mur.ui;
  const win = WINDOWS.find((w) => w[0] === ui.chWin) || WINDOWS[2];
  const now = Date.now();
  const since = { "1h": now - 3600e3, "3h": now - 3 * 3600e3, today: new Date(now).setHours(0, 0, 0, 0), all: -Infinity }[win[0]];
  const at = (m) => m.sortAt ?? when(m.at);

  const agents = new Map();
  for (const d of devices()) for (const se of d.sessions) {
    agents.set(se.name, { name: se.name, d, se, state: sessionState(d, se), sk: NONE, sent: 0, got: 0, partners: new Map() });
  }
  const groups = [];
  for (const st of swarm.streams) {
    const names = st.who.filter((n) => agents.has(n) && agents.get(n).sk === NONE);
    if (!names.length) continue;
    names.forEach((n) => (agents.get(n).sk = st.id));
    groups.push({ key: st.id, stream: st, title: st.title, color: mur.streamColor(st.id), names });
  }
  const loners = [...agents.values()].filter((a) => a.sk === NONE).map((a) => a.name);
  if (loners.length) groups.push({ key: NONE, stream: null, title: "No stream", color: "#8a8a8a", names: loners });
  const G = new Map(groups.map((g, i) => [g.key, Object.assign(g, { i, sent: 0, got: 0, inside: 0, selfN: 0, cross: 0, others: new Map() })]));

  const left = { settled: 0, all: 0, device: 0 };
  const links = new Map(); // "from\0to" -> { from, to, msgs }
  const inWin = [];
  for (const m of messages) {
    const t = at(m);
    if (t < since) continue;
    if (m.toKind === "all") { left.all++; continue; }
    if (m.toKind === "device") { left.device++; continue; }
    if (m.toKind !== "session") continue;
    const f = fromName(m), to = toName(m);
    const A = agents.get(f), B = agents.get(to);
    if (!A || !B) { left.settled++; continue; }
    if (A === B) continue;
    const k = f + "\0" + to;
    let l = links.get(k);
    if (!l) links.set(k, (l = { from: f, to, msgs: [] }));
    l.msgs.push(m);
    inWin.push(m);
    A.sent++; B.got++;
    const pa = A.partners.get(to) || { sent: 0, got: 0, msgs: [] };
    pa.sent++; pa.msgs.push(m); A.partners.set(to, pa);
    const pb = B.partners.get(f) || { sent: 0, got: 0, msgs: [] };
    pb.got++; pb.msgs.push(m); B.partners.set(f, pb);
    const ga = G.get(A.sk), gb = G.get(B.sk);
    ga.sent++; gb.got++;
    if (ga === gb) ga.selfN++;
    if (ga === gb && ga.key !== NONE) ga.inside++;
    else { ga.cross++; if (gb !== ga) gb.cross++; }
    if (ga !== gb) {
      ga.others.set(gb.key, (ga.others.get(gb.key) || 0) + 1);
      gb.others.set(ga.key, (gb.others.get(ga.key) || 0) + 1);
    }
  }
  for (const g of groups) g.all = g.sent + g.got - g.selfN;
  const byTime = (a, b) => at(a) - at(b);
  inWin.sort(byTime);
  for (const l of links.values()) l.msgs.sort(byTime);
  for (const a of agents.values()) for (const p of a.partners.values()) p.msgs.sort(byTime);

  // Stream pairs, unordered: "a|b" with a's index <= b's.
  const pairs = new Map();
  for (const l of links.values()) {
    const ga = G.get(agents.get(l.from).sk), gb = G.get(agents.get(l.to).sk);
    const [x, y] = ga.i <= gb.i ? [ga, gb] : [gb, ga];
    const k = x.key + "|" + y.key;
    let p = pairs.get(k);
    if (!p) pairs.set(k, (p = { key: k, a: x.key, b: y.key, ab: 0, ba: 0, msgs: [] }));
    if (ga === x) p.ab += l.msgs.length; else p.ba += l.msgs.length;
    p.msgs.push(...l.msgs);
  }
  for (const p of pairs.values()) { p.msgs.sort(byTime); p.n = p.msgs.length; p.self = p.a === p.b; }
  const pairOf = (a, b) => {
    const ga = G.get(a), gb = G.get(b);
    if (!ga || !gb) return null;
    return pairs.get(ga.i <= gb.i ? a + "|" + b : b + "|" + a) || null;
  };
  const total = inWin.length;
  // Talk crosses streams unless both agents are in the same stream (two agents in no stream
  // aren't working on one thing together, so their talk counts as crossing).
  const inside = groups.reduce((t, g) => t + g.inside, 0);
  return { win, at, agents, groups, G, links, pairs, pairOf, inWin, total, inside, cross: total - inside, left };
}

// Does a directed agent-to-agent link belong to the selection?
function litLink(M, sel, f, t) {
  if (!sel) return true;
  const gf = M.agents.get(f)?.sk, gt = M.agents.get(t)?.sk;
  if (sel.t === "pair") return (gf === sel.a && gt === sel.b) || (gf === sel.b && gt === sel.a);
  if (sel.t === "stream") return gf === sel.id || gt === sel.id;
  if (sel.t === "agent") return f === sel.name || t === sel.name;
  if (sel.t === "cell") return f === sel.from && t === sel.to;
  return true;
}

function validSel(M, sel) {
  if (!sel) return null;
  if (sel.t === "pair") return M.G.has(sel.a) && M.G.has(sel.b) ? sel : null;
  if (sel.t === "stream") return M.G.has(sel.id) ? sel : null;
  if (sel.t === "agent") return M.agents.has(sel.name) ? sel : null;
  if (sel.t === "cell") return M.agents.has(sel.from) && M.agents.has(sel.to) ? sel : null;
  return null;
}
const sameSel = (x, y) => !!x && !!y && JSON.stringify(x) === JSON.stringify(y);

// ---------- Tooltip: one per chart box, filled with text nodes ----------
function makeTip(h, box) {
  const tip = h("div", { class: "ch-tip", role: "tooltip" });
  box.append(tip);
  const place = (x, y) => {
    const bw = box.clientWidth, tw = tip.offsetWidth, th = tip.offsetHeight;
    let left = x + 14, top = y + 16;
    if (left + tw > bw - 4) left = Math.max(4, x - tw - 14);
    if (top + th > box.clientHeight - 4) top = Math.max(4, y - th - 12);
    tip.style.transform = `translate(${Math.round(left)}px, ${Math.round(top)}px)`;
  };
  return {
    show(ev, rows) {
      tip.replaceChildren(...rows);
      tip.classList.add("on");
      const r = box.getBoundingClientRect();
      if (ev.clientX != null && ev.type !== "focus") place(ev.clientX - r.left, ev.clientY - r.top);
      else {
        const t = ev.target.getBoundingClientRect();
        place(t.left - r.left + t.width / 2, t.top - r.top + t.height / 2);
      }
    },
    move(ev) {
      if (!tip.classList.contains("on")) return;
      const r = box.getBoundingClientRect();
      place(ev.clientX - r.left, ev.clientY - r.top);
    },
    hide() { tip.classList.remove("on"); },
  };
}

// Wire hover, focus, click and Enter on one mark.
function mark(el, tip, { rows, on, off, click, key }) {
  el.setAttribute("tabindex", "0");
  el.setAttribute("role", "button");
  if (key) el.dataset.key = key;
  const enter = (ev) => { on?.(); tip.show(ev, rows()); };
  const leave = () => { off?.(); tip.hide(); };
  el.addEventListener("pointerenter", enter);
  el.addEventListener("pointermove", (ev) => tip.move(ev));
  el.addEventListener("pointerleave", leave);
  // Focus shows the tooltip for keyboard users only; a click re-renders and restores focus,
  // and a tooltip left over the chart after a tap would hide it.
  el.addEventListener("focus", (ev) => { if (el.matches(":focus-visible")) enter(ev); });
  el.addEventListener("blur", leave);
  el.addEventListener("click", (ev) => { ev.stopPropagation(); click(); });
  el.addEventListener("keydown", (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); click(); } });
}

function build(mur) {
  const { h, avatar, plural, ago } = mur;
  const ui = mur.ui;
  const narrow = window.innerWidth < 760;
  lastNarrow = narrow;
  const M = model(mur);
  const form = FORMS.find((f) => f[0] === ui.chForm)?.[0] || "streams";
  const sel = validSel(M, ui.chSel);
  const pick = (v) => { ui.chSel = sameSel(sel, v) ? null : v; mur.render(); };
  const winText = M.win[0] === "all" ? "so far" : M.win[0] === "today" ? "today" : "in the " + M.win[1].toLowerCase();
  const gTitle = (k) => M.G.get(k)?.title || "No stream";
  const at = M.at;

  // Shared bits of tooltip and panel
  const tipHead = (txt) => h("div", { class: "ch-tip-h" }, txt);
  const tipVal = (v, label) => h("div", { class: "ch-tip-r" }, h("b", null, v), " ", label);
  const tipKey = (color, txt) => h("div", { class: "ch-tip-r" }, h("i", { class: "ch-tip-key", style: `background:${color}` }), txt);
  const tipLatest = (m) => m ? h("div", { class: "ch-tip-l" }, "Latest, " + ago(at(m)) + ": ", h("span", null, m.subject || "(no subject)")) : null;
  const sw = (k) => h("i", { class: "ch-sw", style: `background:${M.G.get(k)?.color || "#8a8a8a"}`, "aria-hidden": "true" });
  const linkPair = (a, b, text) => h("a", { class: "ch-link", href: "#", onclick: (ev) => { ev.preventDefault(); pick({ t: "pair", a, b }); } }, text || (a === b ? gTitle(a) : gTitle(a) + " and " + gTitle(b)));
  const linkStream = (k) => h("a", { class: "ch-link", href: "#", onclick: (ev) => { ev.preventDefault(); pick({ t: "stream", id: k }); } }, gTitle(k));
  const linkAgent = (n) => h("a", { class: "ch-link", href: "#", onclick: (ev) => { ev.preventDefault(); pick({ t: "agent", name: n }); } }, n);
  const toWho = (m) => mur.toName(m);

  const box = h("div", { class: "ch-chart ch-" + form });
  const tip = makeTip(h, box);
  box.addEventListener("click", (ev) => { if (!ev.target.closest?.("[role=button]") && sel) pick(null); });

  let chart;
  if (!M.total) {
    chart = h("div", { class: "ch-empty" }, h("p", { class: "ch-empty-t" }, "No messages between running sessions " + winText + "."), h("p", { class: "fx-muted" }, "Try a longer window."));
  } else if (form === "streams") chart = chordForm();
  else if (form === "agents") chart = ringForm();
  else chart = narrow ? pairList() : matrixForm();
  box.prepend(chart);

  // ---------- Streams: chord diagram ----------
  function chordForm() {
    const live = M.groups.filter((g) => g.sent + g.got > 0);
    const n = live.length;
    const idx = new Map(live.map((g, i) => [g.key, i]));
    // Symmetric: a ribbon is as wide as the messages between two streams, either way, and an arc is
    // its streams' messages sent and received. Direction is in the tooltip and the panel.
    const mat = live.map(() => new Array(n).fill(0));
    for (const p of M.pairs.values()) {
      const i = idx.get(p.a), j = idx.get(p.b);
      mat[i][j] += p.n;
      if (i !== j) mat[j][i] += p.n;
    }
    const W = narrow ? 400 : 770, RO = narrow ? 188 : 204, RI = RO - (narrow ? 13 : 14);
    const LF = 14, LN = 12.5, LINE = 17; // label sizes in viewBox units
    const chords = d3.chord().padAngle(n > 1 ? (narrow ? 0.05 : 0.035) : 0).sortSubgroups(d3.descending).sortChords(d3.descending)(mat);
    const arc = d3.arc().innerRadius(RI).outerRadius(RO).cornerRadius(2);
    const ribbon = d3.ribbon().radius(RI - 1.5).padAngle(0.002);

    // Labels outside the arcs, a long title on two lines, nudged apart on each side so they
    // never collide.
    const labels = [];
    if (!narrow) {
      for (const gr of chords.groups) {
        const g = live[gr.index];
        const a = (gr.startAngle + gr.endAngle) / 2;
        const side = Math.sin(a) >= 0 ? 1 : -1;
        const r = RO + 16;
        const room = W / 2 - Math.max(RO + 30, Math.abs(Math.sin(a)) * r) - 8;
        let lines = [g.title];
        if (textWidth(g.title, LF, 500) > room) {
          // Two lines, split where the longer line is shortest.
          const words = g.title.split(" ");
          let best = 1, bestW = Infinity;
          for (let k = 1; k < words.length; k++) {
            const w = Math.max(textWidth(words.slice(0, k).join(" "), LF, 500), textWidth(words.slice(k).join(" "), LF, 500));
            if (w < bestW) { bestW = w; best = k; }
          }
          lines = [words.slice(0, best).join(" "), words.slice(best).join(" ")];
        }
        const hgt = lines.length * LINE + 14;
        labels.push({ g, gr, a, side, lines, hgt, x0: Math.sin(a) * (RO + 4), y0: -Math.cos(a) * (RO + 4), x: Math.sin(a) * r, y: -Math.cos(a) * r - hgt / 2 + 12, ideal: -Math.cos(a) * r - hgt / 2 + 12 });
      }
      for (const side of [1, -1]) {
        const col = labels.filter((l) => l.side === side).sort((p, q) => p.y - q.y);
        for (let it = 0; it < 60; it++) {
          let moved = false;
          for (let i = 1; i < col.length; i++) {
            const d = col[i].y - col[i - 1].y, need = col[i - 1].hgt + 4;
            if (d < need) { const push = (need - d) / 2; col[i].y += push; col[i - 1].y -= push; moved = true; }
          }
          if (!moved) break;
        }
        // Keep labels clear of the arcs at the top and bottom, where the circle bulges out.
        for (const l of col) {
          const yMid = l.y - 12 + l.hgt / 2;
          const near = Math.min(Math.abs(l.y - 12), Math.abs(l.y - 12 + l.hgt), Math.abs(yMid) < l.hgt / 2 ? 0 : Infinity);
          const need = Math.sqrt(Math.max(0, (RO + 12) ** 2 - near ** 2));
          l.x = side * Math.max(Math.abs(Math.sin(l.a) * (RO + 16)), need + 8);
        }
      }
    }
    const ys = labels.flatMap((l) => [l.y - 16, l.y - 12 + l.hgt]);
    const top = Math.min(-RO - 6, ...ys), bot = Math.max(RO + 6, ...ys);
    const H = Math.ceil(bot - top);
    const svg = s("svg", { class: "ch-svg ch-chord-svg" + (sel ? " ch-pinned" : ""), viewBox: `${-W / 2} ${Math.floor(top)} ${W} ${H}`, role: "group", "aria-label": "Chord diagram of messages between streams of work" });
    const ribG = s("g", { class: "ch-ribbons" });
    const arcG = s("g", { class: "ch-arcs" });
    const labG = s("g", { class: "ch-labels" });
    svg.append(ribG, arcG, labG);

    const ribbons = [];
    const lit = (p) => !sel || [...M.links.values()].some((l) => litLink(M, sel, l.from, l.to) && M.pairOf(M.agents.get(l.from).sk, M.agents.get(l.to).sk) === p);
    const hover = (pred) => { svg.classList.add("ch-hovering"); for (const r of ribbons) r.el.classList.toggle("ch-hot", pred(r.p)); };
    const unhover = () => { svg.classList.remove("ch-hovering"); for (const r of ribbons) r.el.classList.remove("ch-hot"); };

    for (const c of chords) {
      const ga = live[c.source.index], gb = live[c.target.index];
      const p = M.pairOf(ga.key, gb.key);
      // Coloured by the stream that wrote more of it.
      const lead = p.self ? M.G.get(p.a) : p.ab >= p.ba ? M.G.get(p.a) : M.G.get(p.b);
      const el = s("path", { class: "ch-ribbon" + (lit(p) ? " ch-lit" : "") + (sel?.t === "pair" && M.pairOf(sel.a, sel.b) === p ? " ch-sel" : ""), d: ribbon(c), fill: lead.color });
      ribbons.push({ el, p });
      const A = M.G.get(p.a), B = M.G.get(p.b);
      mark(el, tip, {
        key: "ch-r-" + p.key,
        rows: () => p.self
          ? [tipHead("Inside " + A.title), tipVal(p.n, plural(p.n, "message").replace(/^\d+ /, "") + " between its " + A.names.length + " agents"), tipLatest(p.msgs.at(-1))]
          : [tipHead(A.title + " and " + B.title), tipVal(p.n, p.n === 1 ? "message" : "messages"), tipKey(A.color, p.ab + " from " + A.title), tipKey(B.color, p.ba + " from " + B.title), tipLatest(p.msgs.at(-1))],
        on: () => hover((q) => q === p),
        off: unhover,
        click: () => pick({ t: "pair", a: p.a, b: p.b }),
      });
      el.setAttribute("aria-label", (p.self ? "Inside " + A.title : A.title + " and " + B.title) + ": " + plural(p.n, "message"));
      ribG.append(el);
    }
    for (const gr of chords.groups) {
      const g = live[gr.index];
      const isSel = sel?.t === "stream" && sel.id === g.key;
      const el = s("path", { class: "ch-arc" + (isSel ? " ch-sel" : ""), d: arc(gr), fill: g.color });
      mark(el, tip, {
        key: "ch-a-" + g.key,
        rows: () => [tipHead(g.title), tipVal(g.all, "messages sent and received"),
          h("div", { class: "ch-tip-r ch-tip-2" }, g.cross + " with other streams" + (g.inside ? ", " + g.inside + " inside" : "")),
          h("div", { class: "ch-tip-r ch-tip-2" }, g.names.join(", "))],
        on: () => hover((q) => q.a === g.key || q.b === g.key),
        off: unhover,
        click: () => pick({ t: "stream", id: g.key }),
      });
      el.setAttribute("aria-label", g.title + ": " + plural(g.all, "message"));
      arcG.append(el);
    }
    for (const l of labels) {
      const anchor = l.side > 0 ? "start" : "end";
      const moved = Math.abs(l.y - l.ideal) > 4;
      if (moved) labG.append(s("path", { class: "ch-leader", d: `M${f1(l.x0)} ${f1(l.y0)} L${f1(l.x - l.side * 4)} ${f1(l.y - 4)}` }));
      const isSel = (sel?.t === "stream" && sel.id === l.g.key) || (sel?.t === "pair" && (sel.a === l.g.key || sel.b === l.g.key));
      const t = s("text", { class: "ch-label" + (l.g.key === NONE ? " ch-label-none" : "") + (isSel ? " ch-sel" : ""), x: f1(l.x), y: f1(l.y), "text-anchor": anchor },
        l.lines.map((ln, i) => s("tspan", { class: "ch-label-t", x: f1(l.x), dy: i ? LINE : 0, style: `font-size:${LF}px` }, ln)),
        s("tspan", { class: "ch-label-n", x: f1(l.x), dy: LINE, style: `font-size:${LN}px` }, plural(l.g.all, "message")));
      t.addEventListener("click", (ev) => { ev.stopPropagation(); pick({ t: "stream", id: l.g.key }); });
      labG.append(t);
    }
    return h("div", { class: "ch-figure" }, svg, legendStreams(live));
  }

  // ---------- Agents: hierarchical edge bundling ----------
  function ringForm() {
    const R = narrow ? 118 : 206;
    const NAME = narrow ? 17 : 12.5;
    const maxName = Math.max(...[...M.agents.keys()].map((n) => textWidth(n, NAME, 500)));
    const bandIn = R + 10 + maxName + 10, bandOut = bandIn + (narrow ? 7 : 8);
    const BL = narrow ? 0 : 13; // band label size; on a phone the legend names the streams
    const ext = bandOut + (BL ? 2 * BL + 14 : 4);
    const W = Math.ceil(ext * 2 + 8);
    const data = { name: "", children: M.groups.map((g) => ({ name: g.key, g, children: g.names.map((n) => ({ name: n, a: M.agents.get(n) })) })) };
    const root = d3.hierarchy(data);
    d3.cluster().size([2 * Math.PI, R]).separation((a, b) => (a.parent === b.parent ? 1 : 2))(root);
    const leaves = new Map(root.leaves().map((l) => [l.data.name, l]));
    const pt = (n, dx = 0, dy = 0) => [n.y * Math.sin(n.x) + dx, -n.y * Math.cos(n.x) + dy];
    const line = d3.line().curve(d3.curveBundle.beta(0.85));
    const maxC = Math.max(1, ...[...M.links.values()].map((l) => l.msgs.length));

    const svg = s("svg", { class: "ch-svg ch-ring-svg" + (sel ? " ch-pinned" : ""), viewBox: `${-W / 2} ${-W / 2} ${W} ${W}`, role: "group", "aria-label": "Agents on a ring, grouped by stream, joined by the messages between them" });
    const linkG = s("g", { class: "ch-links" });
    const nameG = s("g", { class: "ch-names" });
    const bandG = s("g", { class: "ch-bands" });
    svg.append(bandG, linkG, nameG);

    // Each direction is its own lane: the inner control points shift a few pixels to the sender's
    // left, so a conversation both ways reads as two curves that meet at the agents.
    const linkEls = [];
    const nameEls = new Map();
    const hoverLinks = (toneOf, names) => {
      svg.classList.add("ch-hovering");
      for (const { el, l } of linkEls) {
        const t = toneOf(l);
        el.classList.toggle("ch-hot", !!t);
        el.classList.toggle("ch-hsent", t === " ch-sent");
        el.classList.toggle("ch-hrecv", t === " ch-recv");
      }
      for (const [n, el] of nameEls) el.classList.toggle("ch-hot", !!names?.has(n));
    };
    const unhover = () => {
      svg.classList.remove("ch-hovering");
      for (const { el } of linkEls) el.classList.remove("ch-hot", "ch-hsent", "ch-hrecv");
      for (const el of nameEls.values()) el.classList.remove("ch-hot");
    };

    const sorted = [...M.links.values()].sort((a, b) => a.msgs.length - b.msgs.length);
    for (const l of sorted) {
      const A = leaves.get(l.from), B = leaves.get(l.to);
      const pa = pt(A), pb = pt(B);
      const dx = pb[0] - pa[0], dy = pb[1] - pa[1], len = Math.hypot(dx, dy) || 1;
      const off = narrow ? 2.4 : 3.2;
      const nx = (-dy / len) * off, ny = (dx / len) * off;
      const path = A.path(B);
      const pts = path.map((n, i) => (i === 0 || i === path.length - 1 ? pt(n) : pt(n, nx, ny)));
      const d = line(pts);
      const k = l.msgs.length / maxC;
      const isLit = litLink(M, sel, l.from, l.to);
      const tone = sel?.t === "agent" ? (l.from === sel.name ? " ch-sent" : l.to === sel.name ? " ch-recv" : "") : "";
      const g = s("g", { class: "ch-link-g" + (isLit ? " ch-lit" : "") + tone });
      g.append(
        s("path", { class: "ch-curve", d, "stroke-width": f1((narrow ? 0.9 : 1.1) + k * (narrow ? 2.4 : 3)), "stroke-opacity": f1(0.22 + k * 0.5) }),
        s("path", { class: "ch-curve-hit", d }));
      linkEls.push({ el: g, l });
      const rev = M.links.get(l.to + "\0" + l.from);
      mark(g, tip, {
        rows: () => [tipHead(l.from + " to " + l.to), tipVal(l.msgs.length, l.msgs.length === 1 ? "message" : "messages"), rev ? h("div", { class: "ch-tip-r ch-tip-2" }, rev.msgs.length + " back the other way") : null, tipLatest(l.msgs.at(-1))],
        on: () => hoverLinks((x) => (x === l ? " ch-sent" : "")),
        off: unhover,
        click: () => pick({ t: "cell", from: l.from, to: l.to }),
      });
      g.removeAttribute("tabindex"); // the curves are many and thin; keyboard users get the agents
      linkG.append(g);
    }
    for (const [name, leaf] of leaves) {
      const a = leaf.data.a;
      const deg = (leaf.x * 180) / Math.PI - 90;
      const flip = leaf.x > Math.PI;
      const isSel = sel?.t === "agent" && sel.name === name;
      const isLit = !sel || [...M.links.values()].some((l) => (l.from === name || l.to === name) && litLink(M, sel, l.from, l.to)) || isSel
        || (sel?.t === "stream" && a.sk === sel.id) || (sel?.t === "pair" && (a.sk === sel.a || a.sk === sel.b));
      const g = s("g", { class: "ch-name" + (isLit ? " ch-lit" : "") + (isSel ? " ch-sel" : "") + (a.sent + a.got ? "" : " ch-quiet"), transform: `rotate(${f1(deg)}) translate(${R + 8},0)${flip ? " rotate(180)" : ""}` });
      g.append(
        s("circle", { class: "ch-dot ch-dot-" + a.state, cx: flip ? 2 : -2, r: narrow ? 2.6 : 2.2 }),
        s("text", { x: flip ? -6 : 6, dy: "0.32em", "text-anchor": flip ? "end" : "start", style: `font-size:${NAME}px` }, name),
        s("rect", { class: "ch-name-hit", x: flip ? -maxName - 10 : -4, y: -NAME * 0.75, width: maxName + 14, height: NAME * 1.5 }));
      nameEls.set(name, g);
      const st = M.G.get(a.sk);
      mark(g, tip, {
        key: "ch-n-" + name,
        rows: () => [tipHead(name), h("div", { class: "ch-tip-r ch-tip-2" }, (st.key === NONE ? "In no stream" : st.title) + " · " + STATE_TEXT[a.state]),
          tipKey(SENT, plural(a.sent, "message") + " sent"), tipKey(RECV, a.got + " received"),
          h("div", { class: "ch-tip-r ch-tip-2" }, a.partners.size ? "With " + plural(a.partners.size, "agent") : "No messages " + winText)],
        on: () => hoverLinks((l) => (l.from === name ? " ch-sent" : l.to === name ? " ch-recv" : ""), new Set([name, ...a.partners.keys()])),
        off: unhover,
        click: () => pick({ t: "agent", name }),
      });
      g.setAttribute("aria-label", name + ", " + (st.key === NONE ? "no stream" : st.title) + ", " + plural(a.sent, "message") + " sent, " + a.got + " received");
      nameG.append(g);
    }

    // The stream band, outside the names, with its title along it where it fits.
    const unit = root.leaves().length > 1 ? Math.abs(root.leaves()[1].x - root.leaves()[0].x) : Math.PI;
    const bandArc = d3.arc().innerRadius(bandIn).outerRadius(bandOut).cornerRadius(2);
    const defs = s("defs");
    svg.prepend(defs);
    for (const gn of root.children) {
      const g = gn.data.g;
      const xs = gn.leaves().map((l) => l.x);
      const a0 = Math.min(...xs) - unit * 0.45, a1 = Math.max(...xs) + unit * 0.45;
      const isSel = (sel?.t === "stream" && sel.id === g.key) || (sel?.t === "pair" && (sel.a === g.key || sel.b === g.key));
      const band = s("path", { class: "ch-band" + (isSel ? " ch-sel" : ""), d: bandArc({ startAngle: a0, endAngle: a1 }), fill: g.color });
      mark(band, tip, {
        key: "ch-b-" + g.key,
        rows: () => [tipHead(g.title), tipVal(g.all, "messages sent and received"), h("div", { class: "ch-tip-r ch-tip-2" }, g.names.join(", "))],
        on: () => hoverLinks((l) => (M.agents.get(l.from).sk === g.key ? " ch-sent" : M.agents.get(l.to).sk === g.key ? " ch-recv" : ""), new Set(g.names)),
        off: unhover,
        click: () => pick({ t: "stream", id: g.key }),
      });
      bandG.append(band);
      if (!BL) continue;
      // Title along the band: one line, or two if it's long; on the lower half the text runs
      // the other way round so it stays upright.
      const mid = (a0 + a1) / 2;
      const lower = mid > Math.PI / 2 && mid < (3 * Math.PI) / 2;
      const r1 = bandOut + 6 + BL * 0.75;
      const avail = (a1 - a0) * r1 - 4;
      const words = g.title.split(" ");
      let lines = [g.title];
      if (textWidth(g.title, BL, 500) > avail) {
        lines = [""];
        for (const w of words) {
          const tryL = (lines.at(-1) + " " + w).trim();
          if (textWidth(tryL, BL, 500) <= avail || !lines.at(-1)) lines[lines.length - 1] = tryL;
          else lines.push(w);
        }
        if (lines.length > 2) lines = [lines[0], lines.slice(1).join(" ")];
        lines = lines.map((t) => {
          if (textWidth(t, BL, 500) <= avail) return t;
          while (t.length > 1 && textWidth(t + "…", BL, 500) > avail) t = t.slice(0, -1);
          return t + "…";
        });
      }
      lines.forEach((txt, li) => {
        // Upper half: first line nearest the band. Lower half: the text is upside-down otherwise,
        // so it runs counter-clockwise, and its baseline sits on the far side.
        const r = lower ? r1 + li * (BL + 2) + BL * 0.25 : r1 + (lines.length - 1 - li) * (BL + 2);
        const id = "ch-bp-" + gn.data.name.replace(/[^\w-]/g, "") + "-" + li;
        const p0 = [r * Math.sin(a0), -r * Math.cos(a0)], p1 = [r * Math.sin(a1), -r * Math.cos(a1)];
        const big = a1 - a0 > Math.PI ? 1 : 0;
        const d = lower ? `M${f1(p1[0])} ${f1(p1[1])} A${r} ${r} 0 ${big} 0 ${f1(p0[0])} ${f1(p0[1])}` : `M${f1(p0[0])} ${f1(p0[1])} A${r} ${r} 0 ${big} 1 ${f1(p1[0])} ${f1(p1[1])}`;
        defs.append(s("path", { id, d }));
        const tp = s("textPath", { href: "#" + id, startOffset: "50%", "text-anchor": "middle" }, txt);
        bandG.append(s("text", { class: "ch-band-t" + (g.key === NONE ? " ch-label-none" : "") + (isSel ? " ch-sel" : ""), style: `font-size:${BL}px` }, tp));
      });
    }
    return h("div", { class: "ch-figure" }, svg,
      h("div", { class: "ch-legend" },
        legendItems(M.groups),
        h("span", { class: "ch-key" }, h("i", { class: "ch-key-line", style: `background:${SENT}` }), "Sent by the agent you point at or pick"),
        h("span", { class: "ch-key" }, h("i", { class: "ch-key-line", style: `background:${RECV}` }), "Received by it")));
  }

  // ---------- Matrix: senders by receivers ----------
  function matrixForm() {
    const order = M.groups.flatMap((g) => g.names);
    const n = order.length;
    const pos = new Map(order.map((nm, i) => [nm, i]));
    const C = n > 12 ? 30 : 38;
    const FS = 12.5;
    const LW = Math.ceil(Math.max(...order.map((nm) => textWidth(nm, FS, 500)))) + 14;
    const BAND = 5, GAPB = 4;
    const X0 = LW + BAND + GAPB, Y0 = LW + BAND + GAPB;
    const W = X0 + n * C + 2, H = Y0 + n * C + 2;
    const maxC = Math.max(1, ...[...M.links.values()].map((l) => l.msgs.length));
    const color = (c) => RAMP[Math.round(((maxC === 1 ? 1 : (c - 1) / (maxC - 1)) * (RAMP.length - 1)))];
    const svg = s("svg", { class: "ch-svg ch-matrix-svg" + (sel ? " ch-pinned" : ""), viewBox: `0 0 ${W} ${H}`, width: W, role: "group", "aria-label": "Matrix of messages: rows wrote to columns" });
    svg.append(s("text", { class: "ch-corner", x: LW - 2, y: LW - 22, "text-anchor": "end" }, "Rows wrote"), s("text", { class: "ch-corner", x: LW - 2, y: LW - 6, "text-anchor": "end" }, "to columns"));
    const rowLab = new Map(), colLab = new Map();
    for (const nm of order) {
      const i = pos.get(nm), a = M.agents.get(nm);
      const isSel = sel?.t === "agent" && sel.name === nm;
      const rl = s("text", { class: "ch-mlab" + (isSel ? " ch-sel" : "") + (a.sent + a.got ? "" : " ch-quiet"), x: LW, y: Y0 + i * C + C / 2, dy: "0.32em", "text-anchor": "end", style: `font-size:${FS}px` }, nm);
      const cl = s("text", { class: "ch-mlab" + (isSel ? " ch-sel" : "") + (a.sent + a.got ? "" : " ch-quiet"), transform: `translate(${X0 + i * C + C / 2} ${LW}) rotate(-90)`, dy: "0.32em", "text-anchor": "start", style: `font-size:${FS}px` }, nm);
      for (const el of [rl, cl]) {
        el.addEventListener("click", (ev) => { ev.stopPropagation(); pick({ t: "agent", name: nm }); });
      }
      rowLab.set(nm, rl); colLab.set(nm, cl);
      svg.append(rl, cl);
    }
    // Stream bands on both axes, and an outline round each stream's own block on the diagonal.
    let off = 0;
    for (const g of M.groups) {
      const len = g.names.length * C;
      const bandR = s("rect", { class: "ch-mband", x: LW + 4, y: Y0 + off + 1, width: BAND, height: len - 2, rx: 2, fill: g.color });
      const bandC = s("rect", { class: "ch-mband", x: X0 + off + 1, y: LW + 4, width: len - 2, height: BAND, rx: 2, fill: g.color });
      for (const b of [bandR, bandC]) {
        mark(b, tip, { rows: () => [tipHead(g.title), h("div", { class: "ch-tip-r ch-tip-2" }, g.names.join(", "))], click: () => pick({ t: "stream", id: g.key }) });
        b.removeAttribute("tabindex");
      }
      svg.append(bandR, bandC);
      if (off) {
        svg.append(s("line", { class: "ch-msep", x1: X0 - 2, x2: X0 + n * C, y1: Y0 + off, y2: Y0 + off }), s("line", { class: "ch-msep", y1: Y0 - 2, y2: Y0 + n * C, x1: X0 + off, x2: X0 + off }));
      }
      off += len;
    }
    off = 0;
    const blocks = [];
    for (const g of M.groups) {
      const len = g.names.length * C;
      if (g.key !== NONE && g.names.length > 1) blocks.push(s("rect", { class: "ch-mblock", x: X0 + off + 0.5, y: Y0 + off + 0.5, width: len - 1, height: len - 1, rx: 5, stroke: g.color }));
      off += len;
    }
    const cells = s("g", { class: "ch-cells" });
    svg.append(cells, ...blocks);
    const cellEls = [];
    const hoverCell = (f, t) => {
      svg.classList.add("ch-hovering");
      for (const c of cellEls) c.el.classList.toggle("ch-hot", c.f === f && c.t === t);
      for (const [nm, el] of rowLab) el.classList.toggle("ch-hot", nm === f);
      for (const [nm, el] of colLab) el.classList.toggle("ch-hot", nm === t);
    };
    const unhover = () => {
      svg.classList.remove("ch-hovering");
      for (const c of cellEls) c.el.classList.remove("ch-hot");
      for (const el of [...rowLab.values(), ...colLab.values()]) el.classList.remove("ch-hot");
    };
    for (const f of order) for (const t of order) {
      const i = pos.get(f), j = pos.get(t);
      const l = M.links.get(f + "\0" + t);
      const c = l ? l.msgs.length : 0;
      const isLit = litLink(M, sel, f, t);
      const isSel = sel?.t === "cell" && sel.from === f && sel.to === t;
      const rect = s("rect", { class: "ch-cell" + (c ? " ch-full" : "") + (isLit ? " ch-lit" : "") + (isSel ? " ch-sel" : ""), x: X0 + j * C + 1, y: Y0 + i * C + 1, width: C - 2, height: C - 2, rx: 3, style: c ? `fill:${color(c)}` : null });
      cellEls.push({ el: rect, f, t });
      if (f === t) { rect.classList.add("ch-self"); cells.append(rect); continue; }
      const rev = M.links.get(t + "\0" + f);
      mark(rect, tip, {
        key: c ? "ch-c-" + f + "-" + t : null,
        rows: () => [tipHead(f + " to " + t), tipVal(c, c === 1 ? "message" : "messages"), h("div", { class: "ch-tip-r ch-tip-2" }, (rev ? rev.msgs.length : 0) + " back the other way"),
          h("div", { class: "ch-tip-r ch-tip-2" }, M.agents.get(f).sk === M.agents.get(t).sk && M.agents.get(f).sk !== NONE ? "Inside " + gTitle(M.agents.get(f).sk) : gTitle(M.agents.get(f).sk) + " to " + gTitle(M.agents.get(t).sk)),
          l ? tipLatest(l.msgs.at(-1)) : null],
        on: () => hoverCell(f, t),
        off: unhover,
        click: () => (c ? pick({ t: "cell", from: f, to: t }) : pick(null)),
      });
      if (!c) rect.removeAttribute("tabindex");
      cells.append(rect);
    }
    return h("div", { class: "ch-figure" },
      h("div", { class: "ch-mscroll" }, svg),
      h("div", { class: "ch-legend" }, legendItems(M.groups),
        h("span", { class: "ch-key ch-ramp-key" }, h("span", { class: "ch-ramp-n" }, "1"), h("span", { class: "ch-ramp", style: `background:linear-gradient(90deg, ${RAMP.join(", ")})` }),
          h("span", { class: "ch-ramp-n" }, plural(maxC, "message") + " from one agent to another"))));
  }

  // On a phone the matrix becomes the stream pairs, ranked.
  function pairList() {
    const list = [...M.pairs.values()].sort((a, b) => b.n - a.n || (a.self ? 1 : 0) - (b.self ? 1 : 0));
    const max = list[0]?.n || 1;
    return h("div", { class: "ch-figure" },
      h("p", { class: "fx-muted ch-pl-note" }, "Pairs of streams by messages between them, most first."),
      h("ul", { class: "ch-pl" }, list.map((p) => {
        const isSel = sel?.t === "pair" && M.pairOf(sel.a, sel.b) === p;
        return h("li", null, h("button", { class: "ch-pl-row" + (isSel ? " on" : ""), onclick: () => pick({ t: "pair", a: p.a, b: p.b }) },
          h("span", { class: "ch-pl-sw" }, sw(p.a), p.self ? null : sw(p.b)),
          h("span", { class: "ch-pl-name" }, p.self ? "Inside " + gTitle(p.a) : gTitle(p.a) + " and " + gTitle(p.b)),
          h("span", { class: "ch-pl-bar" }, h("span", { style: `width:${(p.n / max) * 100}%` })),
          h("span", { class: "ch-pl-n" }, p.n)));
      })));
  }

  function legendItems(groups) {
    return groups.map((g) => h("button", { class: "ch-key ch-key-s" + ((sel?.t === "stream" && sel.id === g.key) ? " on" : ""), onclick: () => pick({ t: "stream", id: g.key }) }, h("i", { class: "ch-key-sw", style: `background:${g.color}` }), g.title));
  }
  function legendStreams(live) {
    const quiet = M.groups.filter((g) => !live.includes(g));
    return h("div", { class: "ch-legend" }, narrow ? legendItems(live) : null,
      h("span", { class: "ch-faint" }, "Wider ribbon, more messages. A ribbon that returns to its own arc is talk inside one stream."),
      quiet.length ? h("span", { class: "ch-faint" }, "Quiet " + winText + ": " + quiet.map((g) => g.title).join(", ") + ".") : null);
  }

  // ---------- The panel ----------
  const msgRow = (m) => h("button", { class: "ch-msg", onclick: () => mur.swOpenMessage(m) },
    h("span", { class: "ch-t" }, ago(at(m))),
    h("span", { class: "ch-who" }, mur.fromName(m) + " to " + toWho(m)),
    h("span", { class: "ch-subj" }, m.subject || "(no subject)"));
  const msgList = (msgs, cap = 40) => {
    const rev = [...msgs].reverse();
    return [h("div", { class: "ch-msgs" }, rev.slice(0, cap).map(msgRow)), rev.length > cap ? h("p", { class: "fx-muted" }, "And " + (rev.length - cap) + " older.") : null];
  };
  const closeBtn = () => h("button", { class: "fx-icon-btn ch-close", "aria-label": "Clear selection", onclick: () => pick(null) }, mur.icon("x"));
  const bar = (v, max) => h("span", { class: "ch-bar" }, h("span", { style: `width:${(v / Math.max(1, max)) * 100}%` }));

  let panel;
  if (sel?.t === "pair") {
    const p = M.pairOf(sel.a, sel.b);
    const A = M.G.get(sel.a), B = M.G.get(sel.b);
    const self = sel.a === sel.b;
    const pairsOfAgents = new Map();
    for (const m of p?.msgs || []) {
      const k = [mur.fromName(m), toWho(m)].sort().join("\0");
      pairsOfAgents.set(k, (pairsOfAgents.get(k) || 0) + 1);
    }
    const ranked = [...pairsOfAgents].sort((x, y) => y[1] - x[1]);
    const [x, y] = p ? [M.G.get(p.a), M.G.get(p.b)] : [A, B];
    panel = h("div", { class: "ch-panel-in" },
      h("div", { class: "ch-ph" }, h("span", { class: "ch-ph-sw" }, sw(A.key), self ? null : sw(B.key)),
        h("div", null, h("h2", null, self ? "Inside " + A.title : A.title + " and " + B.title), h("p", { class: "ch-sub" }, plural(p?.n || 0, "message") + " " + winText)), closeBtn()),
      !p ? h("p", { class: "fx-muted" }, "They haven't written to each other " + winText + ".")
        : [
          self ? null : h("div", { class: "ch-ways" },
            h("p", null, sw(x.key), h("b", null, p.ab), " from ", x.title),
            h("p", null, sw(y.key), h("b", null, p.ba), " from ", y.title)),
          h("h3", null, "Through"),
          h("ul", { class: "ch-rank" }, ranked.map(([k, c]) => {
            const [a1, a2] = k.split("\0");
            return h("li", null, h("span", { class: "ch-rn" }, linkAgent(a1), " and ", linkAgent(a2)), bar(c, ranked[0][1]), h("span", { class: "ch-c" }, c));
          })),
          h("h3", null, "The messages"),
          msgList(p.msgs),
        ]);
  } else if (sel?.t === "stream") {
    const g = M.G.get(sel.id);
    const others = [...g.others].sort((a, b) => b[1] - a[1]);
    const mine = M.inWin.filter((m) => g.names.includes(mur.fromName(m)) || g.names.includes(toWho(m)));
    const st = g.stream;
    panel = h("div", { class: "ch-panel-in" },
      h("div", { class: "ch-ph" }, h("span", { class: "ch-ph-sw" }, sw(g.key)),
        h("div", null, h("h2", null, g.title), h("p", { class: "ch-sub" }, [st?.pr ? "PR #" + st.pr : st?.issue ? "Issue #" + st.issue : null, plural(g.names.length, "agent"), plural(mine.length, "message") + " " + winText].filter(Boolean).join(" · "))), closeBtn()),
      st?.now ? h("p", { class: "ch-now" }, st.now) : g.key === NONE ? h("p", { class: "ch-now" }, "Agents that haven't said which piece of work they're on.") : null,
      h("p", { class: "ch-agents" }, g.names.map((n, i) => [i ? ", " : "", linkAgent(n)])),
      h("h3", null, "Talks with"),
      others.length || g.inside
        ? h("ul", { class: "ch-rank" },
            others.map(([k, c]) => h("li", null, h("span", { class: "ch-rn" }, sw(k), linkPair(g.key, k, gTitle(k))), bar(c, Math.max(others[0]?.[1] || 0, g.inside)), h("span", { class: "ch-c" }, c))),
            g.inside ? h("li", null, h("span", { class: "ch-rn" }, sw(g.key), linkPair(g.key, g.key, "Inside the stream")), bar(g.inside, Math.max(others[0]?.[1] || 0, g.inside)), h("span", { class: "ch-c" }, g.inside)) : null)
        : h("p", { class: "fx-muted" }, "Nobody " + winText + "."),
      mine.length ? [h("h3", null, "Latest messages"), msgList(mine, 8)] : null);
  } else if (sel?.t === "agent") {
    const a = M.agents.get(sel.name);
    const ranked = [...a.partners].sort((x, y) => (y[1].sent + y[1].got) - (x[1].sent + x[1].got) || x[0].localeCompare(y[0]));
    const max = ranked[0] ? ranked[0][1].sent + ranked[0][1].got : 1;
    panel = h("div", { class: "ch-panel-in" },
      h("div", { class: "ch-ph" }, avatar(a.name),
        h("div", null, h("h2", null, a.name), h("p", { class: "ch-sub" }, mur.fxDot(a.state), STATE_TEXT[a.state], " · ", a.d.owner ? a.d.owner + "'s " : "", a.se.agent || "session")), closeBtn()),
      h("p", { class: "ch-kv" }, h("span", null, "Stream"), a.sk === NONE ? h("b", { class: "ch-warn" }, "None") : h("b", null, sw(a.sk), linkStream(a.sk))),
      h("p", { class: "ch-kv" }, h("span", null, "Doing"), h("b", null, a.se.doing || "Nothing set")),
      h("p", { class: "ch-kv" }, h("span", null, "Messages"), h("b", null, h("span", { class: "ch-sent-t" }, a.sent + " sent"), " · ", h("span", { class: "ch-recv-t" }, a.got + " received"))),
      h("h3", null, "Conversations"),
      ranked.length
        ? h("ul", { class: "ch-convs" }, ranked.map(([n, p]) => {
            const other = M.agents.get(n);
            return h("li", null, h("button", { class: "ch-conv", onclick: () => mur.swOpenMessage(p.msgs.at(-1)) },
              avatar(n),
              h("span", { class: "ch-conv-main" }, h("span", { class: "ch-conv-n" }, n, other.sk !== a.sk || a.sk === NONE ? h("span", { class: "ch-conv-s" }, sw(other.sk), gTitle(other.sk)) : null),
                h("span", { class: "ch-conv-l" }, p.msgs.at(-1).subject || "(no subject)")),
              h("span", { class: "ch-conv-c" }, h("span", { class: "ch-sent-t", title: "sent" }, p.sent + "↑"), h("span", { class: "ch-recv-t", title: "received" }, p.got + "↓"))));
          }))
        : h("p", { class: "fx-muted" }, "No messages with another running session " + winText + "."),
      ranked.length ? h("p", { class: "fx-muted" }, "Each opens the latest message of that conversation.") : null);
  } else if (sel?.t === "cell") {
    const l = M.links.get(sel.from + "\0" + sel.to);
    const rev = M.links.get(sel.to + "\0" + sel.from);
    const fa = M.agents.get(sel.from), ta = M.agents.get(sel.to);
    panel = h("div", { class: "ch-panel-in" },
      h("div", { class: "ch-ph" }, h("span", { class: "ch-pair" }, avatar(sel.from), avatar(sel.to)),
        h("div", null, h("h2", null, sel.from + " to " + sel.to), h("p", { class: "ch-sub" }, plural(l?.msgs.length || 0, "message") + " " + winText)), closeBtn()),
      h("p", { class: "ch-kv" }, h("span", null, sel.from), h("b", null, sw(fa.sk), gTitle(fa.sk))),
      h("p", { class: "ch-kv" }, h("span", null, sel.to), h("b", null, sw(ta.sk), gTitle(ta.sk))),
      rev ? h("p", { class: "ch-kv" }, h("span", null, "Other way"), h("b", null, h("a", { class: "ch-link", href: "#", onclick: (ev) => { ev.preventDefault(); pick({ t: "cell", from: sel.to, to: sel.from }); } }, plural(rev.msgs.length, "message") + " back"))) : null,
      l ? [h("h3", null, "The messages"), msgList(l.msgs)] : h("p", { class: "fx-muted" }, "None " + winText + "."));
  } else {
    panel = reading();
  }

  function reading() {
    const items = [];
    const pct = M.total ? Math.round((M.cross / M.total) * 100) : 0;
    items.push(h("li", null, h("b", null, plural(M.total, "message")), " passed between running sessions " + winText + ". ",
      M.cross ? [h("b", { class: "ch-xtext" }, pct + "%"), " of " + (M.total === 1 ? "it" : "them") + " crossed from one stream to another."] : "None crossed streams."));
    const cross = [...M.pairs.values()].filter((p) => !p.self && p.a !== NONE && p.b !== NONE).sort((a, b) => b.n - a.n);
    if (cross[0]) {
      const p = cross[0];
      items.push(h("li", null, "Most entangled: ", linkPair(p.a, p.b), ", " + plural(p.n, "message") + ".",
        cross[1] && cross[1].n === p.n ? " Tied with " : null, cross[1] && cross[1].n === p.n ? [linkPair(cross[1].a, cross[1].b), "."] : null));
    }
    const real = M.groups.filter((g) => g.key !== NONE);
    const reach = (g) => [...g.others.keys()].filter((k) => k !== NONE).length;
    const hub = [...real].sort((a, b) => reach(b) - reach(a) || b.cross - a.cross)[0];
    if (hub && reach(hub) > 1) {
      const n = reach(hub);
      items.push(h("li", null, linkStream(hub.key), " talks with the most other streams: " + (n === real.length - 1 ? "all " + n : n + " of " + (real.length - 1)) + "."));
    }
    const ins = [...real].filter((g) => g.inside).sort((a, b) => b.inside - a.inside)[0];
    if (ins) items.push(h("li", null, linkPair(ins.key, ins.key), " talks most within itself: " + plural(ins.inside, "message") + " among its " + ins.names.length + " agents."));
    for (const n of M.G.get(NONE)?.names || []) {
      const a = M.agents.get(n);
      const streams = new Set([...a.partners.keys()].map((x) => M.agents.get(x).sk).filter((k) => k !== NONE));
      if (streams.size >= 2) items.push(h("li", null, linkAgent(n), " is in no stream, yet talks with agents in " + streams.size + " streams."));
    }
    const quiet = M.groups.filter((g) => !g.sent && !g.got && g.key !== NONE);
    if (quiet.length && M.total) items.push(h("li", null, "Nothing to or from ", quiet.map((g, i) => [i ? (i === quiet.length - 1 ? " or " : ", ") : "", linkStream(g.key)]), " " + winText + "."));
    const L = M.left;
    const parts = [L.settled ? plural(L.settled, "message") + " with sessions that have since stopped" : null, L.all ? L.all + " to everyone" : null, L.device ? L.device + " to a whole device" : null].filter(Boolean);
    return h("div", { class: "ch-panel-in" },
      h("h2", null, "Reading the streams"),
      h("ul", { class: "ch-read" }, items),
      parts.length ? h("p", { class: "fx-muted ch-left" }, "Left out: " + parts.join(", ").replace(/, ([^,]*)$/, ", and $1") + ".") : null,
      h("p", { class: "fx-muted" }, form === "streams" ? "Click a ribbon or an arc to list its messages." : form === "agents" ? "Point at an agent to light its talk; click to pin it." : narrow ? "Click a pair to list its messages." : "Click a cell to list its messages."));
  }

  const agentsN = M.agents.size;
  const sub = plural(M.total, "message") + " " + winText + " between " + agentsN + " running agents on " + plural(M.groups.filter((g) => g.key !== NONE).length, "stream") + ".";
  return h("div", { class: "fx ch" },
    h("header", { class: "fx-head" },
      h("div", null, h("h1", null, "Between streams"), h("p", null, sub)),
      h("div", { class: "ch-controls" },
        mur.fxSeg("Form", FORMS, form, (v) => { ui.chForm = v; mur.render(); }),
        mur.fxSeg("Time window", WINDOWS, M.win[0], (v) => { ui.chWin = v; mur.render(); }))),
    h("div", { class: "ch-body" },
      box,
      h("aside", { class: "ch-panel", "aria-live": "polite" }, panel)));
}

window.MURVIEWS ||= {};
window.MURVIEWS.chord = (mur) => build(mur);
window.MUR?.render?.();
