// #183 view: "Who talks to whom". A communication map of the swarm, drawn in SVG.
// Ideas borrowed from: the agent-graph views of LangGraph Studio and Langfuse (nodes are agents,
// edges are the calls between them, thickness is how much), TeamClaude's agent topology (who sits
// where, who is a hub), and Palantir's "who is touching what" (a click isolates one agent's world).
// Sessions are grouped into soft regions by stream of work, so talk that crosses a region border
// stands out (purple). Layout is a fixed grid, so the map never jumps between renders.
const SVGNS = "http://www.w3.org/2000/svg";
const WINDOWS = [["1h", "Last hour", 60 * 60000], ["3h", "Last 3 hours", 3 * 60 * 60000], ["all", "Everything", Infinity]];
const LIVE_MS = 10 * 60000;
const STATE_COLOR = { on: "#64af4c", idle: "#ffce2f", dnd: "#ff705d", off: "#6b6b6b" };
const STATE_TEXT = { on: "Active", idle: "Idle", dnd: "Do not disturb", off: "Offline" };

function s(tag, attrs, ...kids) {
  const el = document.createElementNS(SVGNS, tag);
  for (const [k, v] of Object.entries(attrs || {})) if (v != null && v !== false) el.setAttribute(k, v);
  for (const k of kids.flat()) if (k != null && k !== false) el.append(k);
  return el;
}

// Rerender when the window crosses the phone breakpoint, because the map is laid out differently.
let lastNarrow = null;
window.addEventListener("resize", () => {
  const n = window.innerWidth < 760;
  if (lastNarrow !== null && n !== lastNarrow) window.MUR?.render?.();
});

function build(mur) {
  const { h, avatar, plural, ago, devices, messages, swarm, swStreamOf, sessionState, fromName, toName, when } = mur;
  const ui = mur.ui;
  const win = WINDOWS.find((w) => w[0] === ui.gxWin) || WINDOWS[1];
  const now = Date.now();
  const at = (m) => m.sortAt ?? when(m.at);

  // Running sessions
  const nodes = new Map();
  for (const d of devices()) {
    for (const se of d.sessions) {
      nodes.set(se.name, { name: se.name, d, se, state: sessionState(d, se), stream: swStreamOf(se.name) || null, sent: 0, got: 0, bcast: 0, bcastLive: false, partners: new Map() });
    }
  }

  // Messages inside the window between two running sessions, plus broadcasts from running ones
  const edges = new Map();
  const inWin = messages.filter((m) => win[2] === Infinity || now - at(m) <= win[2]).sort((a, b) => at(a) - at(b));
  const bcasts = [];
  for (const m of inWin) {
    const from = fromName(m);
    const a = nodes.get(from);
    if (m.toKind === "all") {
      if (a) { a.bcast++; if (now - at(m) < LIVE_MS) a.bcastLive = true; bcasts.push(m); }
      continue;
    }
    if (m.toKind === "device") { if (a) a.sent++; continue; }
    if (m.toKind !== "session") continue;
    const to = toName(m);
    const b = nodes.get(to);
    if (!a || !b || a === b) continue;
    const pair = [from, to].sort();
    const key = pair.join("|");
    let e = edges.get(key);
    if (!e) edges.set(key, (e = { key, a: pair[0], b: pair[1], msgs: [], live: null }));
    e.msgs.push(m);
    a.sent++; b.got++;
    a.partners.set(to, (a.partners.get(to) || 0) + 1);
    b.partners.set(from, (b.partners.get(from) || 0) + 1);
    if (now - at(m) < LIVE_MS) e.live = { from, to };
  }
  const edgeList = [...edges.values()];
  for (const e of edgeList) {
    const sa = nodes.get(e.a).stream, sb = nodes.get(e.b).stream;
    e.cross = !sa || !sb || sa.id !== sb.id;
  }
  const edgeOf = (x, y) => edges.get([x, y].sort().join("|"));

  // Layout: streams on a grid, the busiest in the middle; agents in no stream in the last cell.
  const narrow = window.innerWidth < 760;
  lastNarrow = narrow;
  const cols = narrow ? 2 : 3;
  const CW = narrow ? 230 : 240, CH = narrow ? 170 : 165, GAP = narrow ? 14 : 20, PAD = narrow ? 8 : 10;
  const groups = swarm.streams
    .map((st) => ({ id: st.id, stream: st, title: st.title, names: st.who.filter((n) => nodes.has(n)) }))
    .filter((g) => g.names.length);
  // The layout goes by all messages, not the chosen window, so the map holds still when you switch.
  const allPairs = new Map();
  for (const m of messages) {
    if (m.toKind !== "session") continue;
    const f = fromName(m), to = toName(m);
    if (!nodes.has(f) || !nodes.has(to) || f === to) continue;
    const k = [f, to].sort().join("|");
    allPairs.set(k, (allPairs.get(k) || 0) + 1);
  }
  const pairList = [...allPairs].map(([k, c]) => [...k.split("|"), c]);
  const deg = (g) => g.names.reduce((t, n) => t + pairList.filter((q) => q[0] === n || q[1] === n).length, 0);
  groups.sort((x, y) => deg(y) - deg(x));
  const loners = [...nodes.values()].filter((n) => !n.stream).map((n) => n.name);
  const total = groups.length + (loners.length ? 1 : 0);
  const rows = Math.ceil(total / cols);
  const cells = [];
  for (let r = 0; r < rows; r++) for (let c = 0; c < cols; c++) cells.push([r, c]);
  const cr = (rows - 1) / 2, cc = (cols - 1) / 2;
  const lonerCell = loners.length ? cells.reduce((best, x) => (x[0] > best[0] || (x[0] === best[0] && x[1] > best[1]) ? x : best)) : null;
  const free = cells.filter((x) => x !== lonerCell).sort((p, q) => Math.hypot(p[0] - cr, p[1] - cc) - Math.hypot(q[0] - cr, q[1] - cc) || p[0] - q[0] || p[1] - q[1]);
  // Try every arrangement of up to 8 streams and keep the one with the least talk stretched
  // across the map (message count times distance). Ties keep the first, so it is deterministic.
  let order = groups.map((_, i) => i);
  if (groups.length <= 8 && groups.length > 2) {
    const streamIdx = new Map();
    groups.forEach((g, i) => g.names.forEach((n) => streamIdx.set(n, i)));
    const links = new Map();
    for (const [x, y, c] of pairList) {
      const i = streamIdx.get(x), j = streamIdx.get(y);
      if (i == null || j == null || i === j) continue;
      const k = Math.min(i, j) + "," + Math.max(i, j);
      links.set(k, (links.get(k) || 0) + c);
    }
    const linkList = [...links].map(([k, c]) => [...k.split(",").map(Number), c]);
    let best = Infinity;
    const cost = (perm) => linkList.reduce((t, [i, j, c]) => t + c * Math.hypot(free[perm[i]][0] - free[perm[j]][0], free[perm[i]][1] - free[perm[j]][1]), 0);
    const heap = (arr, n) => {
      if (n === 1) { const c = cost(arr); if (c < best - 1e-9) { best = c; order = [...arr]; } return; }
      for (let i = 0; i < n; i++) { heap(arr, n - 1); const j = n % 2 ? 0 : i; [arr[j], arr[n - 1]] = [arr[n - 1], arr[j]]; }
    };
    heap(groups.map((_, i) => i), groups.length);
  }
  const regions = [];
  groups.forEach((g, i) => regions.push({ ...g, cell: free[order[i]] }));
  if (loners.length) regions.push({ id: "_none", stream: null, title: "In no stream", names: loners, cell: lonerCell });
  const W = cols * CW + (cols - 1) * GAP + PAD * 2;
  const H = rows * CH + (rows - 1) * GAP + PAD * 2;
  for (const rg of regions) {
    rg.x = PAD + rg.cell[1] * (CW + GAP);
    rg.y = PAD + rg.cell[0] * (CH + GAP);
    rg.w = CW; rg.h = CH;
    const n = rg.names.length;
    rg.names.forEach((name, i) => {
      const nd = nodes.get(name);
      const perRow = n > 2 ? 2 : n;
      const col = i % perRow, row = Math.floor(i / perRow);
      const rowsN = Math.ceil(n / perRow);
      nd.x = rg.x + (CW * (col + 1)) / (perRow + 1);
      nd.y = rg.y + 28 + ((CH - 28) * (row + 0.5)) / rowsN - 4;
      nd.r = 19 + Math.min(pairList.filter((q) => q[0] === name || q[1] === name).length, 5) * 1.6;
    });
  }
  const totalMsgs = edgeList.reduce((t, e) => t + e.msgs.length, 0);
  const crossMsgs = edgeList.filter((e) => e.cross).reduce((t, e) => t + e.msgs.length, 0);

  // Selection
  let sel = ui.gxSel || null;
  if (sel && sel.type === "node" && !nodes.has(sel.name)) sel = null;
  if (sel && sel.type === "edge" && !edgeOf(sel.a, sel.b)) sel = null;
  const pick = (v) => { ui.gxSel = v; mur.render(); };
  const same = (x, y) => x && y && x.type === y.type && x.name === y.name && x.a === y.a && x.b === y.b;
  const toggle = (v) => pick(same(sel, v) ? null : v);

  const litNode = (name) => {
    if (!sel) return true;
    if (sel.type === "node") return sel.name === name || nodes.get(sel.name).partners.has(name);
    return sel.a === name || sel.b === name;
  };
  const litEdge = (e) => {
    if (!sel) return true;
    if (sel.type === "node") return e.a === sel.name || e.b === sel.name;
    return e.key === [sel.a, sel.b].sort().join("|");
  };

  // ---- SVG
  const svg = s("svg", { class: "gx-svg", viewBox: `0 0 ${W} ${H}`, role: "group", "aria-label": "Map of which sessions message which" });
  svg.addEventListener("click", (ev) => { if (ev.target === svg || ev.target.classList?.contains("gx-bg")) pick(null); });
  for (const rg of regions) {
    const dim = sel && !rg.names.some(litNode);
    svg.append(
      s("g", { class: "gx-region" + (rg.stream ? "" : " gx-region-alone") + (dim ? " gx-dim" : "") },
        s("rect", { class: "gx-bg", x: rg.x, y: rg.y, width: rg.w, height: rg.h, rx: 16 }),
        s("text", { class: "gx-rtitle", x: rg.x + 14, y: rg.y + 22 }, rg.title),
        s("text", { class: "gx-rsub", x: rg.x + rg.w - 14, y: rg.y + 22, "text-anchor": "end" }, rg.stream ? (rg.stream.pr ? "#" + rg.stream.pr : "") : "no stream")),
    );
  }
  const maxC = Math.max(1, ...edgeList.map((e) => e.msgs.length));
  const edgeG = s("g", { class: "gx-edges" });
  svg.append(edgeG);
  for (const e of edgeList) {
    const A = nodes.get(e.a), B = nodes.get(e.b);
    const dx = B.x - A.x, dy = B.y - A.y, len = Math.hypot(dx, dy) || 1;
    // A gentle curve, so edges meeting at one node don't merge into one line.
    const bow = Math.min(34, len * 0.12);
    const mx = (A.x + B.x) / 2 - (dy / len) * bow, my = (A.y + B.y) / 2 + (dx / len) * bow;
    const P = (p) => p.x.toFixed(1) + " " + p.y.toFixed(1);
    const d = `M${P(A)} Q${mx.toFixed(1)} ${my.toFixed(1)} ${P(B)}`;
    const lit = litEdge(e);
    const isSel = sel && sel.type === "edge" && lit;
    const sw = (1.5 + (e.msgs.length / maxC) * 5.5).toFixed(1);
    const g = s("g", { class: "gx-edge" + (e.cross ? " gx-cross" : "") + (lit ? "" : " gx-dim") + (isSel ? " gx-sel" : ""), tabindex: 0, role: "button", "aria-label": `${e.a} and ${e.b}: ${e.msgs.length} messages` });
    g.append(s("path", { class: "gx-line", d, "stroke-width": sw }), s("path", { class: "gx-hit", d }));
    if (e.live) {
      const path = e.live.from === e.a ? d : `M${P(B)} Q${mx.toFixed(1)} ${my.toFixed(1)} ${P(A)}`;
      g.append(s("circle", { class: "gx-pulse", r: 3.5, style: `offset-path:path('${path}')` }));
    }
    g.addEventListener("click", (ev) => { ev.stopPropagation(); toggle({ type: "edge", a: e.a, b: e.b }); });
    g.addEventListener("keydown", (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); toggle({ type: "edge", a: e.a, b: e.b }); } });
    edgeG.append(g);
  }
  for (const nd of nodes.values()) {
    const lit = litNode(nd.name);
    const isSel = sel && sel.type === "node" && sel.name === nd.name;
    const size = nd.r * 2;
    const g = s("g", { class: "gx-node" + (lit ? "" : " gx-dim") + (isSel ? " gx-sel" : ""), transform: `translate(${nd.x.toFixed(1)} ${nd.y.toFixed(1)})`, tabindex: 0, role: "button", "aria-label": nd.name + ", " + STATE_TEXT[nd.state] });
    if (nd.bcast) g.append(s("circle", { class: "gx-bcast" + (nd.bcastLive ? " gx-live" : ""), r: nd.r + 9 }));
    const face = avatar(nd.name);
    face.style.fontSize = Math.round(nd.r * 0.8) + "px";
    g.append(
      s("circle", { class: "gx-ring" + (nd.state === "off" ? " gx-off" : ""), r: nd.r + 3.5, stroke: STATE_COLOR[nd.state] }),
      s("foreignObject", { class: "gx-face", x: -nd.r, y: -nd.r, width: size, height: size }, face),
      s("text", { class: "gx-name", y: nd.r + 19, "text-anchor": "middle" }, nd.name),
    );
    if (nd.bcast) g.append(s("title", null, `${nd.name} told everyone ${nd.bcast} time${nd.bcast > 1 ? "s" : ""}`));
    g.addEventListener("click", (ev) => { ev.stopPropagation(); toggle({ type: "node", name: nd.name }); });
    g.addEventListener("keydown", (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); toggle({ type: "node", name: nd.name }); } });
    svg.append(g);
  }

  // ---- Panel
  const line = (m, who) => h("button", { class: "gx-line-row", onclick: () => mur.swOpenMessage(m) },
    h("span", { class: "gx-t" }, ago(at(m))),
    h("span", { class: "gx-who" }, who),
    h("span", { class: "gx-subj" }, m.subject || "(no subject)"));
  const stateDot = (st) => h("span", { class: "fx-dot fx-dot-" + st, "aria-hidden": "true" });
  const linkNode = (n) => h("button", { class: "gx-link", onclick: () => pick({ type: "node", name: n.name }) }, n.name);

  let panel;
  if (sel && sel.type === "node") {
    const nd = nodes.get(sel.name);
    const ranked = [...nd.partners.entries()].sort((x, y) => y[1] - x[1] || x[0].localeCompare(y[0]));
    const mine = inWin.filter((m) => fromName(m) === nd.name || (m.toKind === "session" && toName(m) === nd.name)).slice(-5).reverse();
    panel = h("div", { class: "gx-panel-in" },
      h("div", { class: "gx-agent" }, avatar(nd.name),
        h("div", null, h("h2", null, nd.name), h("p", { class: "gx-sub" }, stateDot(nd.state), STATE_TEXT[nd.state], " · ", nd.d.owner ? nd.d.owner + "'s " : "", nd.se.agent || "session"))),
      h("p", { class: "gx-kv" }, h("span", null, "Stream"), nd.stream ? h("b", null, nd.stream.title) : h("b", { class: "gx-warn" }, "None. Hasn't said what it's for.")),
      h("p", { class: "gx-kv" }, h("span", null, "Doing"), h("b", null, nd.se.doing || "Nothing set")),
      h("h3", null, "Talks to most"),
      ranked.length
        ? h("ul", { class: "gx-rank" }, ranked.map(([n, c]) => {
            const e = edgeOf(nd.name, n);
            return h("li", null, h("button", { onclick: () => pick({ type: "edge", a: e.a, b: e.b }) },
              avatar(n), h("span", { class: "gx-rn" }, n, e.cross ? h("i", { class: "gx-xdot", title: "Different streams" }) : null),
              h("span", { class: "gx-bar" }, h("span", { style: `width:${(c / ranked[0][1]) * 100}%` })),
              h("span", { class: "gx-c" }, c)));
          }))
        : h("p", { class: "gx-muted" }, "No messages with another running session " + (win[0] === "all" ? "yet." : "in this window.")),
      nd.bcast ? h("p", { class: "gx-muted" }, "Also told everyone " + plural(nd.bcast, "time") + ".") : null,
      mine.length ? [h("h3", null, "Latest messages"), h("div", { class: "gx-lines" }, mine.map((m) => line(m, fromName(m) === nd.name ? "to " + (m.toKind === "session" ? toName(m) : m.toKind === "all" ? "everyone" : "a device") : "from " + fromName(m))))] : null);
  } else if (sel && sel.type === "edge") {
    const e = edgeOf(sel.a, sel.b);
    const na = nodes.get(e.a), nb = nodes.get(e.b);
    const ca = e.msgs.filter((m) => fromName(m) === e.a).length, cb = e.msgs.length - ca;
    panel = h("div", { class: "gx-panel-in" },
      h("div", { class: "gx-agent" }, h("span", { class: "gx-pair" }, avatar(e.a), avatar(e.b)),
        h("div", null, h("h2", null, e.a + " and " + e.b), h("p", { class: "gx-sub" }, plural(e.msgs.length, "message"), " · ", e.cross ? h("span", { class: "gx-xtext" }, "across streams") : "inside one stream"))),
      h("p", { class: "gx-kv" }, h("span", null, e.a), h("b", null, ca + " sent · " + (na.stream ? na.stream.title : "no stream"))),
      h("p", { class: "gx-kv" }, h("span", null, e.b), h("b", null, cb + " sent · " + (nb.stream ? nb.stream.title : "no stream"))),
      h("h3", null, "The conversation"),
      h("div", { class: "gx-lines" }, [...e.msgs].reverse().map((m) => line(m, fromName(m)))));
  } else {
    // A plain reading of the map
    const busiest = [...edgeList].sort((x, y) => y.msgs.length - x.msgs.length)[0];
    const hub = [...nodes.values()].sort((x, y) => y.partners.size - x.partners.size || (y.sent + y.got) - (x.sent + x.got))[0];
    const silent = [...nodes.values()].filter((n) => n.sent === 0 && n.bcast === 0);
    const unheard = silent.filter((n) => n.got > 0);
    const cut = silent.filter((n) => n.got === 0);
    const pct = totalMsgs ? Math.round((crossMsgs / totalMsgs) * 100) : 0;
    const items = [];
    items.push(h("li", null, h("b", null, plural(totalMsgs, "message")), " passed between running sessions " + (win[0] === "all" ? "so far" : "in the " + win[1].toLowerCase()) + ". ", crossMsgs ? h("span", null, h("b", { class: "gx-xtext" }, pct + "%"), " of it crossed from one stream to another.") : "None of it crossed streams."));
    if (busiest) items.push(h("li", null, "Busiest pair: ", h("button", { class: "gx-link", onclick: () => pick({ type: "edge", a: busiest.a, b: busiest.b }) }, busiest.a + " and " + busiest.b), ", " + busiest.msgs.length + " messages."));
    if (hub && hub.partners.size > 1) {
      const streamIds = new Set([hub.stream?.id || "_", ...[...hub.partners.keys()].map((n) => nodes.get(n).stream?.id || "_")]);
      items.push(h("li", null, "The hub: ", linkNode(hub), " talks with " + hub.partners.size + " others, across " + plural(streamIds.size, "stream") + "."));
    }
    for (const n of unheard) items.push(h("li", null, linkNode(n), " has been written to but hasn't written to anyone" + (n.stream ? "." : ", and belongs to no stream.")));
    for (const n of cut) items.push(h("li", null, linkNode(n), " is isolated: nothing sent or received."));
    if (bcasts.length) items.push(h("li", null, plural(bcasts.length, "broadcast"), " went to everyone; the dashed rings mark who sent them."));
    panel = h("div", { class: "gx-panel-in" },
      h("h2", null, "Reading the map"),
      h("ul", { class: "gx-read" }, items),
      h("p", { class: "gx-muted" }, "Click a session to see who it talks to, or a line to read that conversation."));
  }

  const legend = h("div", { class: "gx-legend" },
    h("span", null, h("i", { class: "gx-sw gx-sw-n" }), "Same stream"),
    h("span", null, h("i", { class: "gx-sw gx-sw-x" }), "Across streams"),
    h("span", null, h("i", { class: "gx-live-dot" }), "Message in the last 10 min"),
    h("span", null, h("i", { class: "gx-sw-ring" }), "Told everyone"),
    h("span", { class: "gx-states" }, ...["on", "idle", "dnd", "off"].map((k) => h("span", { class: "gx-st" }, stateDot(k), STATE_TEXT[k]))),
    h("span", { class: "gx-faint" }, "Thicker line, more messages"));

  return h("div", { class: "fx gx" },
    h("header", { class: "fx-head" },
      h("div", null, h("h1", null, "Who talks to whom"), h("p", null, "Running sessions grouped by stream of work, joined by the messages they sent each other.")),
      mur.fxSeg("Time window", WINDOWS.map((w) => [w[0], w[1]]), win[0], (v) => { ui.gxWin = v; mur.render(); })),
    h("div", { class: "gx-body" },
      h("div", { class: "gx-map" }, svg, legend),
      h("aside", { class: "gx-panel", "aria-live": "polite" }, panel)));
}

window.MURVIEWS ||= {};
window.MURVIEWS.graph = (mur) => build(mur);
window.MUR?.render?.();
