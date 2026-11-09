// #183 view: "Who talks to whom". The swarm as one picture you can read at a glance: agents grouped
// by the work they're on, a line for every pair that talks (thicker, more messages), and what
// they're talking about: under each box's title for talk inside that stream, and on the busiest
// lines for talk between streams. (Labels on the lines inside a box didn't fit between the faces,
// and those were the busiest conversations.) Problems are drawn where they happen, in words:
//   - a yellow arrow and "waiting 20m" on the line from an agent to the one it's waiting on;
//   - a word under an agent in trouble: tests failing, offline, do not disturb, quiet 15m or more.
// Agents of different streams changing one file without having talked about it are listed beside
// the map; the dashed line between them appears when you pick one of them. Drawn all at once, at
// 16 agents, those lines crossed the whole map and buried the talk.
// A slider takes the picture back in time; problems are only known for now.
// Borrowed from: LangGraph Studio's and Langfuse's agent graphs (agents as nodes, calls as edges,
// thickness as volume), TeamClaude's topology, Palantír's "who is touching what" and FASTDash's
// shared-file warnings, and a subway map's habit of naming its lines.
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
const pairKey = (x, y) => [x, y].sort().join("|");
const short = (t, n) => (t.length > n ? t.slice(0, n - 1).trimEnd() + "…" : t);
const dur = (ms) => {
  const m = Math.max(1, Math.round(ms / 60000));
  return m < 60 ? m + "m" : Math.floor(m / 60) + "h" + (m % 60 ? " " + (m % 60) + "m" : "");
};
const clock = (t) => new Date(t).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });

// Rerender when the window crosses the phone breakpoint, because the map is laid out differently.
let lastNarrow = null;
window.addEventListener("resize", () => {
  const n = window.innerWidth < 760;
  if (lastNarrow !== null && n !== lastNarrow) window.MUR?.render?.();
});
let playTimer = null;

// Everything the picture shows, at a moment `back` minutes before now (0 is now).
function model(mur, back) {
  const { devices, messages, swarm, swStreamOf, sessionState, fromName, toName, when } = mur;
  const ui = mur.ui;
  const win = WINDOWS.find((w) => w[0] === ui.gxWin) || WINDOWS[1];
  const now = Date.now();
  const tEnd = now - back * 60000;
  const isNow = back === 0;
  const at = (m) => m.sortAt ?? when(m.at);

  const nodes = new Map();
  for (const d of devices()) {
    for (const se of d.sessions) {
      nodes.set(se.name, { name: se.name, d, se, state: sessionState(d, se), stream: swStreamOf(se.name) || null, started: when(se.started) || 0, sent: 0, got: 0, bcast: 0, partners: new Map(), trouble: null });
    }
  }

  // Every message between two running sessions, by pair: for the layout (so the map holds still)
  // and to tell whether two agents have ever talked about a file.
  const allByPair = new Map();
  for (const m of messages) {
    if (m.toKind !== "session") continue;
    const f = fromName(m), t = toName(m);
    if (!nodes.has(f) || !nodes.has(t) || f === t) continue;
    const k = pairKey(f, t);
    if (!allByPair.has(k)) allByPair.set(k, []);
    allByPair.get(k).push(m);
  }

  // Talk inside the window that ends at the chosen moment.
  const edges = new Map();
  const inWin = messages.filter((m) => at(m) <= tEnd && (win[2] === Infinity || tEnd - at(m) <= win[2])).sort((a, b) => at(a) - at(b));
  let bcasts = 0;
  for (const m of inWin) {
    const from = fromName(m);
    const a = nodes.get(from);
    if (m.toKind === "all") { if (a) { a.bcast++; bcasts++; } continue; }
    if (m.toKind === "device") { if (a) a.sent++; continue; }
    if (m.toKind !== "session") continue;
    const to = toName(m);
    const b = nodes.get(to);
    if (!a || !b || a === b) continue;
    const key = pairKey(from, to);
    let e = edges.get(key);
    if (!e) edges.set(key, (e = { key, a: key.split("|")[0], b: key.split("|")[1], msgs: [], live: null }));
    e.msgs.push(m);
    a.sent++; b.got++;
    a.partners.set(to, (a.partners.get(to) || 0) + 1);
    b.partners.set(from, (b.partners.get(from) || 0) + 1);
    if (isNow && now - at(m) < LIVE_MS) e.live = { from, to };
  }
  const topics = swarm.topics || {};
  for (const e of edges.values()) {
    const sa = nodes.get(e.a).stream, sb = nodes.get(e.b).stream;
    e.cross = !sa || !sb || sa.id !== sb.id;
    const last = e.msgs[e.msgs.length - 1];
    e.topic = topics[e.key] || short((last.subject || "").replace(/^Re: /, ""), 24);
  }

  // Problems, known only for now.
  const waits = [], clashes = [], troubles = [];
  for (const nd of nodes.values()) if (nd.started > tEnd) nd.trouble = { text: "not started yet", tone: "off" };
  if (isNow) {
    const H = mur.history();
    const events = H ? H.events : [];
    const failing = (name) => {
      const runs = new Map();
      for (const e of events) if (e.who === name && e.kind === "tool" && e.tool === "Bash") {
        if (!runs.has(e.detail)) runs.set(e.detail, []);
        runs.get(e.detail).push(e);
      }
      for (const [cmd, list] of runs) {
        const last3 = list.sort((x, y) => Date.parse(x.at) - Date.parse(y.at)).slice(-3);
        if (last3.length === 3 && last3.every((e) => e.ok === false)) return cmd;
      }
      return null;
    };
    for (const nd of nodes.values()) {
      const cmd = failing(nd.name);
      if (cmd) nd.trouble = { text: "tests failing", tone: "bad", cmd };
      else if (nd.state === "off") nd.trouble = { text: "offline " + dur(now - when(nd.d.lastSeen)), tone: "off" };
      else if (nd.state === "dnd") nd.trouble = { text: "do not disturb", tone: "bad" };
      else if (nd.state === "idle" && now - when(nd.se.lastActive) >= 15 * 60000) nd.trouble = { text: "quiet " + dur(now - when(nd.se.lastActive)), tone: "warn" };
    }
    const seen = new Set();
    for (const st of swarm.streams) {
      const w = st.waiting;
      if (!w || !nodes.has(w.who) || !nodes.has(w.on) || seen.has(w.who + ">" + w.on)) continue;
      seen.add(w.who + ">" + w.on);
      waits.push({ ...w, key: pairKey(w.who, w.on) });
    }
    // Two agents of different streams (an agent in no stream is a stream of its own) in one file,
    // who have never mentioned it to each other.
    const group = (n) => nodes.get(n).stream?.id || "none:" + n;
    const byPair = new Map();
    for (const area of swarm.areas || []) {
      for (const [file, who] of area.files) {
        const path = area.path + "/" + file, base = file.split("/").pop();
        const word = new RegExp("(^|[^\\w])" + base.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + "(?![\\w])", "i");
        const here = who.filter((n) => nodes.has(n));
        for (let i = 0; i < here.length; i++) for (let j = i + 1; j < here.length; j++) {
          const x = here[i], y = here[j];
          if (group(x) === group(y)) continue;
          const talked = (allByPair.get(pairKey(x, y)) || []).some((m) => { const t = (m.subject || "") + " " + (m.body || ""); return t.includes(path) || word.test(t); });
          if (talked) continue;
          const k = pairKey(x, y);
          if (!byPair.has(k)) byPair.set(k, { key: k, a: k.split("|")[0], b: k.split("|")[1], files: [] });
          byPair.get(k).files.push(path);
        }
      }
    }
    clashes.push(...byPair.values());
    for (const w of waits) troubles.push({ kind: "wait", w });
    for (const c of clashes) troubles.push({ kind: "clash", c });
    for (const nd of nodes.values()) if (nd.trouble && (nd.trouble.tone === "bad" || nd.state === "off")) troubles.push({ kind: "node", nd });
    for (const nd of nodes.values()) if (nd.sent === 0 && nd.bcast === 0 && nd.got > 0) troubles.push({ kind: "unheard", nd });
  }

  const edgeList = [...edges.values()];
  const totalMsgs = edgeList.reduce((t, e) => t + e.msgs.length, 0);
  const crossMsgs = edgeList.filter((e) => e.cross).reduce((t, e) => t + e.msgs.length, 0);
  return { win, now, tEnd, back, isNow, nodes, edges, edgeList, allByPair, waits, clashes, troubles, totalMsgs, crossMsgs, bcasts, inWin, at };
}

// Streams on a grid, the busiest in the middle, arranged so the most talk crosses the least
// distance; agents in no stream share the last cell. Up to four faces sit two to a row, so names
// don't collide. Laid out from all messages, so nothing moves with the window or the slider.
function layout(M, swarm, narrow) {
  const { nodes, allByPair } = M;
  const cols = narrow ? 2 : 3;
  const CW = narrow ? 176 : 254, GAP = narrow ? 10 : 18, PAD = narrow ? 4 : 8;
  const TITLE = narrow ? 44 : 50, ROWH = narrow ? 78 : 92, BODY = narrow ? 100 : 112;
  const pairList = [...allByPair].map(([k, ms]) => [...k.split("|"), ms.length]);
  const deg = (n) => pairList.filter((q) => q[0] === n || q[1] === n).length;
  const groups = swarm.streams
    .map((st) => ({ id: st.id, stream: st, title: st.title, names: st.who.filter((n) => nodes.has(n)) }))
    .filter((g) => g.names.length);
  groups.sort((x, y) => y.names.reduce((t, n) => t + deg(n), 0) - x.names.reduce((t, n) => t + deg(n), 0));
  const loners = [...nodes.values()].filter((n) => !n.stream).map((n) => n.name);
  const total = groups.length + (loners.length ? 1 : 0);
  const rows = Math.max(1, Math.ceil(total / cols));
  const cells = [];
  for (let r = 0; r < rows; r++) for (let c = 0; c < cols; c++) cells.push([r, c]);
  const cr = (rows - 1) / 2, cc = (cols - 1) / 2;
  const lonerCell = loners.length ? cells[cells.length - 1] : null;
  const free = cells.filter((x) => x !== lonerCell).sort((p, q) => Math.hypot(p[0] - cr, p[1] - cc) - Math.hypot(q[0] - cr, q[1] - cc) || p[0] - q[0] || p[1] - q[1]);
  let order = groups.map((_, i) => i);
  if (groups.length <= 8 && groups.length > 2) {
    const idx = new Map();
    groups.forEach((g, i) => g.names.forEach((n) => idx.set(n, i)));
    const links = new Map();
    for (const [x, y, c] of pairList) {
      const i = idx.get(x), j = idx.get(y);
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
  const regions = groups.map((g, i) => ({ ...g, cell: free[order[i]] }));
  if (loners.length) regions.push({ id: "_none", stream: null, title: "In no stream", names: loners, cell: lonerCell });
  const perRow = (n) => (n <= 1 ? 1 : n <= 4 ? 2 : 3);
  const maxRows = Math.max(1, ...regions.map((rg) => Math.ceil(rg.names.length / perRow(rg.names.length))));
  const CH = TITLE + Math.max(BODY, maxRows * ROWH);
  const W = cols * CW + (cols - 1) * GAP + PAD * 2;
  const H = rows * CH + (rows - 1) * GAP + PAD * 2;
  for (const rg of regions) {
    rg.x = PAD + rg.cell[1] * (CW + GAP);
    rg.y = PAD + rg.cell[0] * (CH + GAP);
    rg.w = CW; rg.h = CH;
    const n = rg.names.length, k = perRow(n), rowsR = Math.ceil(n / k);
    rg.names.forEach((name, i) => {
      const nd = nodes.get(name);
      const row = Math.floor(i / k), col = i % k;
      const inRow = Math.min(k, n - row * k);
      const spread = inRow === 2 ? 0.5 : 0.31;
      nd.x = rg.x + CW * (0.5 + (col - (inRow - 1) / 2) * spread);
      nd.y = rg.y + TITLE + ((CH - TITLE) * (row + 0.5)) / rowsR - (narrow ? 14 : 16);
      nd.r = (narrow ? 13 : 16) + Math.min(deg(name), 5) * (narrow ? 1 : 1.4);
    });
  }
  // Positions live here, not on the model, because the slider builds a new model for each moment.
  const pos = new Map([...nodes.values()].map((nd) => [nd.name, { x: nd.x, y: nd.y, r: nd.r }]));
  return { W, H, regions, narrow, pos, boxes: true };
}

// "Pulled by talk": no boxes. Every pair that has messaged is a spring, pulling harder the more
// they've said, and a back-and-forth conversation pulls twice as hard as messages one way; every
// agent pushes every other away. Run to rest (Fruchterman and Reingold, 1991), the agents that
// talk most end up together, whatever stream they're filed under, and an agent talking to two
// groups sits between them. It starts from a circle in stream order and has no randomness, so the
// same messages always give the same picture. Laid out from all messages, like the boxes.
const RECIPROCAL = 2;
const GRAVITY = 0.45;
let forceCache = null;
function forceLayout(M, narrow, fromName) {
  const { nodes, allByPair } = M;
  const W = narrow ? 360 : 780, H = narrow ? 600 : 540, PAD = narrow ? 42 : 60;
  const names = [...nodes.keys()].sort((a, b) => (nodes.get(a).stream?.id || "~").localeCompare(nodes.get(b).stream?.id || "~") || a.localeCompare(b));
  const idx = new Map(names.map((n, i) => [n, i]));
  const links = [];
  for (const [k, ms] of allByPair) {
    const [a, b] = k.split("|");
    const ab = ms.filter((m) => fromName(m) === a).length, ba = ms.length - ab;
    links.push({ i: idx.get(a), j: idx.get(b), w: (ab + ba) * (ab && ba ? RECIPROCAL : 1) });
  }
  const sig = [narrow, names.join(","), links.map((l) => l.i + "-" + l.j + ":" + l.w).join(",")].join("|");
  if (forceCache?.sig === sig) return forceCache.L;
  const n = names.length;
  const deg = names.map((_, i) => links.filter((l) => l.i === i || l.j === i).length);
  const rad = deg.map((d) => (narrow ? 13 : 16) + Math.min(d, 5) * (narrow ? 1 : 1.4));
  const cx = W / 2, cy = H / 2;
  const p = names.map((_, i) => {
    const t = (2 * Math.PI * i) / n - Math.PI / 2;
    return { x: cx + Math.cos(t) * (W / 2 - PAD), y: cy + Math.sin(t) * (H / 2 - PAD) };
  });
  const k = 1.15 * Math.sqrt(((W - 2 * PAD) * (H - 2 * PAD)) / n);
  const wmax = Math.max(1, ...links.map((l) => l.w));

  // Fit a set of points to the map: centred, scaled per axis but never stretched more than
  // twice as much one way as the other, so groups keep their shape.
  const fit = (pts) => {
    const xs = pts.map((q) => q.x), ys = pts.map((q) => q.y);
    const x0 = Math.min(...xs), x1 = Math.max(...xs), y0 = Math.min(...ys), y1 = Math.max(...ys);
    let fx = (W - 2 * PAD) / Math.max(1, x1 - x0), fy = (H - 2 * PAD) / Math.max(1, y1 - y0);
    fx = Math.min(fx, fy * 2, 2.2);
    fy = Math.min(fy, fx * 2, 2.2);
    return pts.map((q) => ({ x: cx + (q.x - (x0 + x1) / 2) * fx, y: cy + (q.y - (y0 + y1) / 2) * fy }));
  };
  const toMap = (pts) => new Map(names.map((nm, i) => [nm, { x: pts[i].x, y: pts[i].y, r: rad[i] }]));
  const frames = [toMap(p)];

  // The forces, with no walls: agents pressed against the map's edge settle into flat rows along
  // it. Each step of the settling is fitted to the map for the animation.
  let temp = Math.min(W, H) / 5;
  for (let it = 0; it < 320; it++) {
    const dx = new Float64Array(n), dy = new Float64Array(n);
    for (let i = 0; i < n; i++) for (let j = i + 1; j < n; j++) {
      let x = p[i].x - p[j].x, y = p[i].y - p[j].y;
      if (Math.abs(x) + Math.abs(y) < 0.01) { x = 0.01 * (i - j); y = 0.01; }
      const d = Math.hypot(x, y), f = (k * k) / d;
      dx[i] += (x / d) * f; dy[i] += (y / d) * f;
      dx[j] -= (x / d) * f; dy[j] -= (y / d) * f;
    }
    for (const l of links) {
      const x = p[l.i].x - p[l.j].x, y = p[l.i].y - p[l.j].y, d = Math.hypot(x, y) || 0.01;
      const f = ((d * d) / k) * (0.25 + 1.75 * Math.sqrt(l.w / wmax));
      dx[l.i] -= (x / d) * f; dy[l.i] -= (y / d) * f;
      dx[l.j] += (x / d) * f; dy[l.j] += (y / d) * f;
    }
    for (let i = 0; i < n; i++) {
      // A pull toward the middle keeps an agent nobody talks to from drifting off, weaker
      // sideways so the picture spreads to the map's shape.
      dx[i] -= (p[i].x - cx) * GRAVITY * (H / W);
      dy[i] -= (p[i].y - cy) * GRAVITY;
      const d = Math.hypot(dx[i], dy[i]) || 1, step = Math.min(d, temp);
      p[i].x += (dx[i] / d) * step;
      p[i].y += (dy[i] / d) * step;
    }
    temp = Math.max(1, temp * 0.982);
    if (it % 4 === 3) frames.push(toMap(fit(p)));
  }

  // Then, on the map, push apart any two agents whose faces or names would overlap.
  const q = fit(p);
  const sx = narrow ? 88 : 112, sy = narrow ? 70 : 80;
  for (let it = 0; it < 300; it++) {
    let moved = false;
    for (let i = 0; i < n; i++) for (let j = i + 1; j < n; j++) {
      const nx = (q[j].x - q[i].x) / sx, ny = (q[j].y - q[i].y) / sy, e = Math.hypot(nx, ny);
      if (e >= 1) continue;
      moved = true;
      const push = (1 - e) / 2 + 0.005, ux = e ? nx / e : 1, uy = e ? ny / e : 0;
      q[i].x -= ux * push * sx; q[i].y -= uy * push * sy;
      q[j].x += ux * push * sx; q[j].y += uy * push * sy;
    }
    for (const r of q) { r.x = Math.min(W - PAD, Math.max(PAD, r.x)); r.y = Math.min(H - PAD, Math.max(PAD, r.y)); }
    if (it % 5 === 4) frames.push(toMap(q));
    if (!moved) break;
  }
  const pos = toMap(q);
  frames.push(pos);
  const L = { W, H, regions: [], narrow, pos, boxes: false, frames };
  forceCache = { sig, L };
  return L;
}

function build(mur) {
  const { h, avatar, plural, ago, swarm, fromName, toName } = mur;
  const ui = mur.ui;
  const narrow = window.innerWidth < 760;
  lastNarrow = narrow;
  const at = (m) => m.sortAt ?? mur.when(m.at);

  // The slider's reach: from the first running agent's start, at most 8 hours back.
  const first = Math.min(...mur.devices().flatMap((d) => d.sessions.map((se) => mur.when(se.started) || Date.now())));
  const reach = Math.max(10, Math.min(480, Math.ceil((Date.now() - first) / 60000)));
  if (ui.gxT == null || ui.gxT > reach) ui.gxT = 0;

  const pick = (v) => { ui.gxSel = v; stopPlay(); mur.render(); };
  const same = (x, y) => x && y && x.type === y.type && x.name === y.name && x.a === y.a && x.b === y.b;
  const toggle = (v) => pick(same(ui.gxSel, v) ? null : v);
  const linkNode = (name) => h("button", { class: "gx-link", onclick: () => pick({ type: "node", name }) }, name);
  const linkEdge = (e, text) => h("button", { class: "gx-link", onclick: () => pick({ type: "edge", a: e.a, b: e.b }) }, text);
  const msgRow = (m, who) => h("button", { class: "gx-line-row", onclick: () => mur.swOpenMessage(m) },
    h("span", { class: "gx-t" }, ago(at(m))),
    h("span", { class: "gx-who" }, who),
    h("span", { class: "gx-subj" }, m.subject || "(no subject)"));
  const stateDot = (st) => h("span", { class: "fx-dot fx-dot-" + st, "aria-hidden": "true" });

  // ---- The picture
  function svgFor(M, L) {
    const { nodes, edges, edgeList, waits, clashes } = M;
    for (const nd of nodes.values()) Object.assign(nd, L.pos.get(nd.name));
    let sel = ui.gxSel || null;
    if (sel?.type === "node" && !nodes.has(sel.name)) sel = null;
    if (sel?.type === "edge" && !edges.has(pairKey(sel.a, sel.b))) sel = null;
    if (sel?.type === "clash" && !clashes.some((c) => c.key === pairKey(sel.a, sel.b))) sel = null;
    const selKey = sel && sel.type !== "node" ? pairKey(sel.a, sel.b) : null;
    const litNode = (name) => {
      if (!sel) return true;
      if (sel.type === "node") return sel.name === name || nodes.get(sel.name).partners.has(name) || waits.some((w) => (w.who === sel.name && w.on === name) || (w.on === sel.name && w.who === name)) || clashes.some((c) => (c.a === sel.name && c.b === name) || (c.b === sel.name && c.a === name));
      return sel.a === name || sel.b === name;
    };
    const litPair = (a, b) => (!sel ? true : sel.type === "node" ? a === sel.name || b === sel.name : pairKey(a, b) === selKey);

    const svg = s("svg", { class: "gx-svg", viewBox: `0 0 ${L.W} ${L.H}`, role: "group", "aria-label": "Map of which agents message which, and what about" });
    svg.addEventListener("click", (ev) => { if (ev.target === svg || ev.target.classList?.contains("gx-bg")) pick(null); });
    const defs = s("defs", null, s("marker", { id: "gx-arrow", viewBox: "0 0 10 10", refX: 8, refY: 5, markerWidth: 7, markerHeight: 7, orient: "auto-start-reverse" }, s("path", { d: "M0 0 L10 5 L0 10 z", fill: "#ffce2f" })));
    svg.append(defs);
    const obstacles = [];
    // What a stream's own agents are discussing, busiest first, as much as fits under the title.
    const inner = (rg) => {
      if (!rg.stream) return "";
      const list = edgeList.filter((e) => !e.cross && nodes.get(e.a).stream?.id === rg.id).sort((x, y) => y.msgs.length - x.msgs.length);
      const room = Math.floor((rg.w - 28) / (L.narrow ? 5.6 : 6.2));
      let out = "";
      for (const e of list) {
        const next = out ? out + " · " + e.topic : e.topic;
        if (next.length > room) break;
        out = next;
      }
      return out || (list.length ? short(list[0].topic, room) : "");
    };
    for (const rg of L.regions) {
      const dim = sel && !rg.names.some(litNode);
      svg.append(s("g", { class: "gx-region" + (rg.stream ? "" : " gx-region-alone") + (dim ? " gx-dim" : "") },
        s("rect", { class: "gx-bg", x: rg.x, y: rg.y, width: rg.w, height: rg.h, rx: 16 }),
        s("text", { class: "gx-rtitle", x: rg.x + 14, y: rg.y + 22 }, L.narrow ? short(rg.title, 19) : rg.title),
        // A phone's box has no room for the PR number beside the title.
        !L.narrow && s("text", { class: "gx-rsub", x: rg.x + rg.w - 14, y: rg.y + 22, "text-anchor": "end" }, rg.stream?.pr ? "#" + rg.stream.pr : ""),
        inner(rg) && s("text", { class: "gx-rtopics", x: rg.x + 14, y: rg.y + (L.narrow ? 36 : 40) }, inner(rg))));
      obstacles.push({ x: rg.x, y: rg.y + 6, w: Math.min(rg.w, 24 + rg.title.length * 7.6), h: inner(rg) ? 40 : 22 });
    }

    // A gentle curve per pair; a clash bows the other way, so it never hides under a talk line.
    const curve = (A, B, sign = 1) => {
      const dx = B.x - A.x, dy = B.y - A.y, len = Math.hypot(dx, dy) || 1;
      const bow = sign * Math.min(34, len * 0.12);
      const M = { x: (A.x + B.x) / 2 - (dy / len) * bow, y: (A.y + B.y) / 2 + (dx / len) * bow };
      const P = (p) => p.x.toFixed(1) + " " + p.y.toFixed(1);
      const pt = (t) => ({ x: (1 - t) ** 2 * A.x + 2 * (1 - t) * t * M.x + t * t * B.x, y: (1 - t) ** 2 * A.y + 2 * (1 - t) * t * M.y + t * t * B.y });
      return { d: `M${P(A)} Q${P(M)} ${P(B)}`, rev: `M${P(B)} Q${P(M)} ${P(A)}`, pt, A, B, M };
    };
    // Shorten a curve so an arrowhead lands on the target face's edge, not its centre.
    const toward = (c, B, r) => {
      const tx = B.x - c.M.x, ty = B.y - c.M.y, l = Math.hypot(tx, ty) || 1;
      return { x: B.x - (tx / l) * (r + 7), y: B.y - (ty / l) * (r + 7) };
    };

    const pills = [];
    const maxC = Math.max(1, ...edgeList.map((e) => e.msgs.length));
    const lineG = s("g", { class: "gx-edges" });
    svg.append(lineG);
    const waitOf = (key) => waits.find((w) => w.key === key);
    for (const e of edgeList) {
      const A = nodes.get(e.a), B = nodes.get(e.b);
      const c = curve(A, B);
      e.c = c;
      const lit = litPair(e.a, e.b);
      const isSel = sel?.type === "edge" && lit;
      const sw = (1.5 + (e.msgs.length / maxC) * 5.5).toFixed(1);
      const g = s("g", { class: "gx-edge" + (e.cross ? " gx-cross" : "") + (lit ? "" : " gx-dim") + (isSel ? " gx-sel" : ""), tabindex: 0, role: "button", "aria-label": `${e.a} and ${e.b}, ${e.topic}: ${e.msgs.length} messages` });
      g.append(s("path", { class: "gx-line", d: c.d, "stroke-width": sw }), s("path", { class: "gx-hit", d: c.d }), s("title", null, `${e.a} and ${e.b}: ${e.topic} (${plural(e.msgs.length, "message")})`));
      if (e.live) g.append(s("circle", { class: "gx-pulse", r: 3.5, style: `offset-path:path('${e.live.from === e.a ? c.d : c.rev}')` }));
      const w = waitOf(e.key);
      if (w) {
        const target = nodes.get(w.on), from = nodes.get(w.who);
        const cw = curve(from, target);
        const tip = toward(cw, target, target.r);
        g.append(s("path", { class: "gx-waitline", d: `M${from.x.toFixed(1)} ${from.y.toFixed(1)} Q${cw.M.x.toFixed(1)} ${cw.M.y.toFixed(1)} ${tip.x.toFixed(1)} ${tip.y.toFixed(1)}`, "marker-end": "url(#gx-arrow)" }));
      }
      const open = () => toggle({ type: "edge", a: e.a, b: e.b });
      g.addEventListener("click", (ev) => { ev.stopPropagation(); open(); });
      g.addEventListener("keydown", (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); open(); } });
      lineG.append(g);
      // In boxes, talk inside a stream is named under the box's title instead.
      if (w || e.cross || !L.boxes) pills.push({ kind: w ? "wait" : "topic", rank: w ? 0 : 2, n: e.msgs.length, c, lit, isSel, text: w ? (L.narrow ? "waiting " + w.since + "m" : "waiting " + w.since + "m · " + e.topic) : e.topic, alt: w ? "waiting " + w.since + "m" : null, open, key: e.key });
    }
    // A wait with no talk in this window still gets its arrow.
    for (const w of waits) {
      if (edges.has(w.key)) continue;
      const from = nodes.get(w.who), target = nodes.get(w.on);
      const c = curve(from, target);
      const tip = toward(c, target, target.r);
      const lit = litPair(w.who, w.on);
      lineG.append(s("path", { class: "gx-waitline gx-waitonly" + (lit ? "" : " gx-dim"), d: `M${from.x.toFixed(1)} ${from.y.toFixed(1)} Q${c.M.x.toFixed(1)} ${c.M.y.toFixed(1)} ${tip.x.toFixed(1)} ${tip.y.toFixed(1)}`, "marker-end": "url(#gx-arrow)" }));
      pills.push({ kind: "wait", rank: 0, n: 0, c, lit, text: "waiting " + w.since + "m", open: () => toggle({ type: "node", name: w.who }), key: w.key });
    }
    const shown = clashes.filter((cl) => sel && ((sel.type === "clash" && cl.key === selKey) || (sel.type === "node" && (cl.a === sel.name || cl.b === sel.name))));
    for (const cl of shown) {
      const A = nodes.get(cl.a), B = nodes.get(cl.b);
      const c = curve(A, B, -1);
      const lit = litPair(cl.a, cl.b);
      const isSel = sel?.type === "clash" && lit;
      const open = () => toggle({ type: "clash", a: cl.a, b: cl.b });
      const names = cl.files.map((f) => f.split("/").pop());
      const g = s("g", { class: "gx-clash" + (lit ? "" : " gx-dim") + (isSel ? " gx-sel" : ""), tabindex: 0, role: "button", "aria-label": `${cl.a} and ${cl.b} are both in ${names.join(", ")} and haven't talked about it` });
      g.append(s("path", { class: "gx-clashline", d: c.d }), s("path", { class: "gx-hit", d: c.d }), s("title", null, `${cl.a} and ${cl.b} are both in ${names.join(", ")}, and haven't talked about it`));
      g.addEventListener("click", (ev) => { ev.stopPropagation(); open(); });
      g.addEventListener("keydown", (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); open(); } });
      lineG.append(g);
      pills.push({ kind: "clash", rank: 1, n: 0, c, lit, isSel, text: "both in " + names[0] + (names.length > 1 ? " +" + (names.length - 1) : ""), open, key: "clash:" + cl.key });
    }

    // Faces, with a word under any agent in trouble.
    for (const nd of nodes.values()) {
      const lit = litNode(nd.name);
      const isSel = sel?.type === "node" && sel.name === nd.name;
      const future = nd.trouble?.text === "not started yet";
      const g = s("g", { class: "gx-node" + (lit ? "" : " gx-dim") + (isSel ? " gx-sel" : "") + (future ? " gx-future" : ""), transform: `translate(${nd.x.toFixed(1)} ${nd.y.toFixed(1)})`, tabindex: 0, role: "button", "aria-label": nd.name + ", " + (nd.trouble ? nd.trouble.text : STATE_TEXT[nd.state]) });
      const face = avatar(nd.name);
      face.style.fontSize = Math.round(nd.r * 0.8) + "px";
      // The ring is quiet unless something is wrong, and the word under the name says what.
      const ringColor = nd.trouble?.tone === "bad" ? "#ff705d" : nd.trouble?.tone === "warn" ? "#ffce2f" : "#ffffff40";
      g.append(
        s("circle", { class: "gx-ring" + (M.isNow && nd.state === "off" ? " gx-off" : ""), r: nd.r + 3.5, stroke: ringColor }),
        s("foreignObject", { class: "gx-face", x: -nd.r, y: -nd.r, width: nd.r * 2, height: nd.r * 2 }, face),
        s("text", { class: "gx-name", y: nd.r + 18, "text-anchor": "middle" }, nd.name),
        nd.trouble && s("text", { class: "gx-why gx-why-" + nd.trouble.tone, y: nd.r + 32, "text-anchor": "middle" }, nd.trouble.text),
      );
      g.addEventListener("click", (ev) => { ev.stopPropagation(); toggle({ type: "node", name: nd.name }); });
      g.addEventListener("keydown", (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); toggle({ type: "node", name: nd.name }); } });
      svg.append(g);
      obstacles.push({ x: nd.x - nd.r - 6, y: nd.y - nd.r - 6, w: nd.r * 2 + 12, h: nd.r * 2 + 12 });
      const nameW = nd.name.length * 7.2 + 8;
      obstacles.push({ x: nd.x - nameW / 2, y: nd.y + nd.r + 5, w: nameW, h: nd.trouble ? 32 : 17 });
    }

    // Name the lines: waits and clashes always, then the busiest talk, where a label fits without
    // covering a face, a name or another label. The rest name themselves on hover and in the panel.
    const budget = L.narrow ? 3 : 6;
    const fs = L.narrow ? 10 : 11, cw = L.narrow ? 5.5 : 6.1;
    const pillG = s("g", { class: "gx-pills" });
    const hit = (r) => obstacles.some((o) => r.x < o.x + o.w && r.x + r.w > o.x && r.y < o.y + o.h && r.y + r.h > o.y);
    const inside = (r) => r.x >= 0 && r.y >= 0 && r.x + r.w <= L.W && r.y + r.h <= L.H;
    pills.sort((x, y) => x.rank - y.rank || y.n - x.n);
    let placed = 0;
    if (L.settling) pills.length = 0;
    for (const p of pills) {
      const forced = p.rank < 2 || p.isSel;
      if (!forced && (placed >= budget || !p.lit)) continue;
      const hh = fs + 8;
      const overlap = (r) => obstacles.reduce((t, o) => t + Math.max(0, Math.min(r.x + r.w, o.x + o.w) - Math.max(r.x, o.x)) * Math.max(0, Math.min(r.y + r.h, o.y + o.h) - Math.max(r.y, o.y)), 0);
      // On the line if there's room, else just beside it: a short line between two close faces
      // (common when talk pulls them together) has no room on it. A label that must show (a wait)
      // tries its short form, then takes the spot that covers least.
      const { A, B, M: Q } = p.c;
      const spots = (text, offs) => {
        const w = text.length * cw + 16, out = [];
        for (const off of offs) for (const t of [0.5, 0.42, 0.58, 0.35, 0.65, 0.28, 0.72, 0.22, 0.78]) {
          const q = p.c.pt(t);
          const tx = 2 * (1 - t) * (Q.x - A.x) + 2 * t * (B.x - Q.x), ty = 2 * (1 - t) * (Q.y - A.y) + 2 * t * (B.y - Q.y), tl = Math.hypot(tx, ty) || 1;
          const r = { x: q.x - (ty / tl) * off - w / 2, y: q.y + (tx / tl) * off - hh / 2, w, h: hh };
          if (inside(r)) out.push(r);
        }
        return out;
      };
      let spot = null, text = p.text;
      for (const [tx, offs] of [[p.text, [0, 16, -16, 30, -30]], ...(p.alt ? [[p.alt, [0, 16, -16, 30, -30, 46, -46]]] : [])]) {
        spot = spots(tx, offs).find((r) => !hit(r)) || null;
        if (spot) { text = tx; break; }
      }
      if (!spot && forced) {
        text = p.alt || p.text;
        spot = spots(text, [0, 16, -16, 30, -30, 46, -46]).sort((x, y) => overlap(x) - overlap(y))[0] || null;
      }
      if (!spot) continue;
      const w = spot.w;
      obstacles.push(spot);
      if (p.kind === "topic") placed++;
      const g = s("g", { class: "gx-pill gx-pill-" + p.kind + (p.lit ? "" : " gx-dim") + (p.isSel ? " gx-sel" : "") });
      g.append(s("rect", { x: spot.x, y: spot.y, width: w, height: hh, rx: hh / 2 }), s("text", { x: spot.x + w / 2, y: spot.y + hh / 2 + fs * 0.36, "text-anchor": "middle", "font-size": fs }, text));
      g.addEventListener("click", (ev) => { ev.stopPropagation(); p.open(); });
      pillG.append(g);
    }
    svg.append(pillG);
    return svg;
  }

  // ---- The panel beside it
  function panelFor(M) {
    const { nodes, edges, edgeList, waits, clashes, troubles, inWin } = M;
    const sel = ui.gxSel || null;
    if (sel?.type === "node" && nodes.has(sel.name)) {
      const nd = nodes.get(sel.name);
      const ranked = [...nd.partners.entries()].sort((x, y) => y[1] - x[1] || x[0].localeCompare(y[0]));
      const mine = inWin.filter((m) => fromName(m) === nd.name || (m.toKind === "session" && toName(m) === nd.name)).slice(-5).reverse();
      const w = waits.find((x) => x.who === nd.name), wOn = waits.filter((x) => x.on === nd.name);
      const cl = clashes.filter((c) => c.a === nd.name || c.b === nd.name);
      return h("div", { class: "gx-panel-in" },
        h("div", { class: "gx-agent" }, avatar(nd.name),
          h("div", null, h("h2", null, nd.name), h("p", { class: "gx-sub" }, stateDot(nd.state), STATE_TEXT[nd.state], " · ", nd.d.owner ? nd.d.owner + "'s " : "", nd.se.agent || "session"))),
        h("p", { class: "gx-kv" }, h("span", null, "Stream"), nd.stream ? h("b", null, nd.stream.title) : h("b", { class: "gx-warn" }, "None. Hasn't said what it's for.")),
        h("p", { class: "gx-kv" }, h("span", null, "Doing"), h("b", null, nd.se.doing || "Nothing set")),
        nd.trouble?.cmd && h("p", { class: "gx-alert gx-alert-bad" }, h("code", null, nd.trouble.cmd), " has failed its last 3 runs."),
        w && h("p", { class: "gx-alert" }, "Waiting " + w.since + " minutes on ", linkNode(w.on), " for " + w.what + "."),
        wOn.map((x) => h("p", { class: "gx-alert" }, linkNode(x.who), " has waited " + x.since + " minutes on it for " + x.what + ".")),
        cl.map((c) => h("p", { class: "gx-alert" }, "Both in " + c.files.map((f) => f.split("/").pop()).join(", ") + " with ", linkNode(c.a === nd.name ? c.b : c.a), ", and they haven't talked about it.")),
        h("h3", null, "Talks to"),
        ranked.length
          ? h("ul", { class: "gx-rank" }, ranked.map(([n, c]) => {
              const e = edges.get(pairKey(nd.name, n));
              return h("li", null, h("button", { onclick: () => pick({ type: "edge", a: e.a, b: e.b }) },
                avatar(n), h("span", { class: "gx-rn" }, h("b", null, n), h("span", null, e.topic)), h("span", { class: "gx-c" }, c)));
            }))
          : h("p", { class: "gx-muted" }, "No messages with another running agent in this window."),
        mine.length ? [h("h3", null, "Latest messages"), h("div", { class: "gx-lines" }, mine.map((m) => msgRow(m, fromName(m) === nd.name ? "to " + (m.toKind === "session" ? toName(m) : m.toKind === "all" ? "everyone" : "a device") : "from " + fromName(m))))] : null);
    }
    if (sel?.type === "edge" && edges.has(pairKey(sel.a, sel.b))) {
      const e = edges.get(pairKey(sel.a, sel.b));
      const na = nodes.get(e.a), nb = nodes.get(e.b);
      const ca = e.msgs.filter((m) => fromName(m) === e.a).length, cb = e.msgs.length - ca;
      const w = waits.find((x) => x.key === e.key);
      return h("div", { class: "gx-panel-in" },
        h("div", { class: "gx-agent" }, h("span", { class: "gx-pair" }, avatar(e.a), avatar(e.b)),
          h("div", null, h("h2", null, e.topic), h("p", { class: "gx-sub" }, e.a + " and " + e.b + " · " + plural(e.msgs.length, "message")))),
        w && h("p", { class: "gx-alert" }, linkNode(w.who), " has waited " + w.since + " minutes on ", linkNode(w.on), " for " + w.what + "."),
        h("p", { class: "gx-kv" }, h("span", null, e.a), h("b", null, ca + " sent · " + (na.stream ? na.stream.title : "no stream"))),
        h("p", { class: "gx-kv" }, h("span", null, e.b), h("b", null, cb + " sent · " + (nb.stream ? nb.stream.title : "no stream"))),
        h("h3", null, "The conversation"),
        h("div", { class: "gx-lines" }, [...e.msgs].reverse().map((m) => msgRow(m, fromName(m)))));
    }
    if (sel?.type === "clash") {
      const c = clashes.find((x) => x.key === pairKey(sel.a, sel.b));
      if (c) {
        const past = M.allByPair.get(c.key) || [];
        return h("div", { class: "gx-panel-in" },
          h("div", { class: "gx-agent" }, h("span", { class: "gx-pair" }, avatar(c.a), avatar(c.b)),
            h("div", null, h("h2", null, "Both in " + c.files.map((f) => f.split("/").pop()).join(", ")), h("p", { class: "gx-sub" }, c.a + " and " + c.b))),
          h("p", { class: "gx-alert" }, "They're on different work and both changing ", c.files.map((f, i) => [i ? ", " : "", h("code", null, f)]), ". Neither has mentioned it to the other."),
          [c.a, c.b].map((n) => { const nd = nodes.get(n); return h("p", { class: "gx-kv" }, h("span", null, n), h("b", null, (nd.stream ? nd.stream.title : "No stream") + ": " + (nd.se.doing || "nothing set"))); }),
          h("h3", null, past.length ? "What they have talked about" : "They've never messaged each other"),
          past.length ? h("div", { class: "gx-lines" }, [...past].reverse().map((m) => msgRow(m, fromName(m)))) : null);
      }
    }

    // A plain reading of the picture.
    const top = [...edgeList].sort((x, y) => y.msgs.length - x.msgs.length).slice(0, 3);
    const hub = [...nodes.values()].sort((x, y) => y.partners.size - x.partners.size || (y.sent + y.got) - (x.sent + x.got))[0];
    const going = top.map((e) => h("li", null, linkEdge(e, e.a + " and " + e.b), ": " + e.topic + " (" + e.msgs.length + ")"));
    if (hub && hub.partners.size > 2) going.push(h("li", null, linkNode(hub.name), " is the hub: it talks with " + hub.partners.size + " others."));
    if (M.bcasts) going.push(h("li", { class: "gx-quiet" }, plural(M.bcasts, "message") + " went to everyone."));
    const needOf = (list) => list.map((t) => {
      if (t.kind === "wait") return h("li", null, linkNode(t.w.who), " has waited " + t.w.since + " minutes on ", linkNode(t.w.on), " for " + t.w.what + ".");
      if (t.kind === "clash") return h("li", null, linkNode(t.c.a), " and ", linkNode(t.c.b), " are both in ", h("button", { class: "gx-link", onclick: () => pick({ type: "clash", a: t.c.a, b: t.c.b }) }, t.c.files.map((f) => f.split("/").pop()).join(", ")), " and haven't talked about it.");
      if (t.kind === "unheard") return h("li", null, linkNode(t.nd.name), " has been written to and hasn't answered" + (t.nd.stream ? "." : ". Nobody knows what it's for."));
      const nd = t.nd;
      if (nd.trouble.cmd) return h("li", null, linkNode(nd.name), "'s tests have failed three runs in a row.");
      if (nd.state === "off") return h("li", null, linkNode(nd.name), "'s device has been " + nd.trouble.text + ".");
      return h("li", null, linkNode(nd.name), " is on do not disturb.");
    });
    const stuck = troubles.filter((t) => t.kind !== "clash"), shared = troubles.filter((t) => t.kind === "clash");
    return h("div", { class: "gx-panel-in" },
      h("h2", null, "What's going on"),
      going.length ? h("ul", { class: "gx-read" }, going) : h("p", { class: "gx-muted" }, "Nobody talked in this window."),
      M.isNow
        ? [stuck.length ? [h("h2", { class: "gx-h2-gap" }, "Stuck (" + stuck.length + ")"), h("ul", { class: "gx-read gx-need" }, needOf(stuck))] : h("p", { class: "gx-muted" }, "Nothing looks stuck."),
           shared.length ? [h("h2", { class: "gx-h2-gap" }, "Same file, not talked about (" + shared.length + ")"), h("ul", { class: "gx-read gx-need" }, needOf(shared))] : null]
        : h("p", { class: "gx-muted" }, "Problems are known only for now. Drag the slider to the right end to see them."),
      h("p", { class: "gx-muted" }, "Click a line or an agent to read what was said."));
  }

  const subtitle = (M) => {
    const span = M.win[2] === Infinity ? "so far" : "in the " + M.win[1].toLowerCase();
    const stuck = M.troubles.filter((t) => t.kind !== "clash").length;
    return plural(M.nodes.size, "agent") + " · " + plural(M.totalMsgs, "message") + " between them " + (M.isNow ? span : span + " before " + clock(M.tEnd)) + (M.isNow ? " · " + (stuck ? stuck + " stuck" : "nothing stuck") : "");
  };

  // ---- Assemble. The slider repaints the picture in place, so dragging isn't interrupted by a
  // rebuild of the whole page.
  let M = model(mur, ui.gxT);
  const byTalk = ui.gxLayout === "talk";
  const L = byTalk ? forceLayout(M, narrow, fromName) : layout(M, swarm, narrow);
  const sub = h("p", null, subtitle(M));
  const host = h("div", { class: "gx-host" }, svgFor(M, L));
  const panel = h("aside", { class: "gx-panel", "aria-live": "polite" }, panelFor(M));
  const timeText = (back) => (back === 0 ? "Now" : clock(Date.now() - back * 60000) + " · " + dur(back * 60000) + " ago");
  const tLabel = h("span", { class: "gx-tnow" }, timeText(ui.gxT));
  const repaint = () => {
    M = model(mur, ui.gxT);
    host.replaceChildren(svgFor(M, L));
    panel.replaceChildren(panelFor(M));
    sub.textContent = subtitle(M);
    tLabel.textContent = timeText(ui.gxT);
    slider.value = String(reach - ui.gxT);
    playBtn.replaceChildren(mur.icon(playTimer ? "square" : "play"));
    playBtn.setAttribute("aria-label", playTimer ? "Stop" : "Play the last " + dur(reach * 60000));
  };
  function stopPlay() { if (playTimer) { clearInterval(playTimer); playTimer = null; } }
  const slider = h("input", { type: "range", class: "gx-range", min: "0", max: String(reach), step: "1", value: String(reach - ui.gxT), "aria-label": "Show the picture at an earlier time" });
  slider.addEventListener("input", () => { stopPlay(); ui.gxT = reach - Number(slider.value); repaint(); });
  const playBtn = h("button", { class: "gx-play", "aria-label": "Play the last " + dur(reach * 60000) }, mur.icon("play"));
  playBtn.addEventListener("click", () => {
    if (playTimer) { stopPlay(); repaint(); return; }
    if (ui.gxT === 0) ui.gxT = reach;
    const step = Math.max(1, Math.round(reach / 90));
    playTimer = setInterval(() => {
      if (!host.isConnected) { stopPlay(); return; }
      ui.gxT = Math.max(0, ui.gxT - step);
      if (ui.gxT === 0) stopPlay();
      repaint();
    }, 110);
    repaint();
  });

  // Switching to "Pulled by talk" plays the layout settling, once, unless motion is turned down.
  if (L.frames && ui.gxAnimate) {
    ui.gxAnimate = false;
    if (!window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      let i = 0;
      const step = () => {
        if (!host.isConnected && i > 0) return;
        if (i < L.frames.length) { host.replaceChildren(svgFor(M, { ...L, pos: L.frames[i], settling: true })); i++; requestAnimationFrame(step); }
        else host.replaceChildren(svgFor(M, L));
      };
      requestAnimationFrame(step);
    }
  }

  const legend = h("div", { class: "gx-legend" },
    byTalk && h("span", null, h("i", { class: "gx-sw-near" }), "Closer, more talk; back-and-forth counts double"),
    h("span", null, h("i", { class: "gx-sw gx-sw-fat" }), "Thicker line, more messages"),
    h("span", null, h("i", { class: "gx-sw gx-sw-x" }), "Between different streams"),
    h("span", null, h("i", { class: "gx-sw gx-sw-wait" }), "Waiting for an answer"));

  return h("div", { class: "fx gx" },
    h("header", { class: "fx-head" },
      h("div", null, h("h1", null, "Who talks to whom"), sub),
      h("div", { class: "gx-controls" },
        mur.fxSeg("Arrange", [["boxes", "Stream boxes"], ["talk", "Pulled by talk"]], byTalk ? "talk" : "boxes", (v) => { ui.gxLayout = v; ui.gxAnimate = v === "talk"; stopPlay(); mur.render(); }),
        mur.fxSeg("Time window", WINDOWS.map((w) => [w[0], w[1]]), M.win[0], (v) => { ui.gxWin = v; stopPlay(); mur.render(); }))),
    h("div", { class: "gx-body" },
      h("div", { class: "gx-map" },
        host,
        h("div", { class: "gx-time" }, playBtn, h("span", { class: "gx-tend" }, clock(Date.now() - reach * 60000)), slider, tLabel),
        legend),
      panel));
}

window.MURVIEWS ||= {};
window.MURVIEWS.graph = (mur) => build(mur);
window.MUR?.render?.();
