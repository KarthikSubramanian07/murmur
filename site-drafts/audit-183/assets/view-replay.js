// #183 view: "Replay". The swarm's last hours played back like a film, on the same map as "Who
// talks to whom": sessions grouped in regions by stream of work, held still for the whole replay.
// Ideas borrowed from:
//   - Netflix Vizceral: traffic drawn as dots streaming along each line, denser where a pair has
//     talked more lately.
//   - code_swarm (Michael Ogawa, 2008) and Gource: people light up when they act and fade when
//     they go quiet, so activity reads as heat moving around the picture.
// Everything drawn is a pure function of the playhead time, so pausing freezes a frame and the
// scrubber can jump anywhere. Structure (regions, lines, faces) is SVG; what moves is one canvas.
const SVGNS = "http://www.w3.org/2000/svg";
const MIN = 60000;
const WINS = [["1h", "Last hour", 60 * MIN], ["3h", "Last 3 hours", 180 * MIN], ["today", "Today", null]];
const WIN_TEXT = { "1h": "The last hour", "3h": "The last 3 hours", today: "Today" };
const SPEEDS = [1, 5, 15];
const DEF_SPEED = { "1h": 1, "3h": 5, today: 15 };
const BIN = { "1h": 2 * MIN, "3h": 5 * MIN, today: 15 * MIN };
const GREY = "158,158,158";
const PURPLE = "228,150,246";
const REDUCED = window.matchMedia("(prefers-reduced-motion: reduce)");

// One loop at a time, across rebuilds.
let active = null;

// Rerender when the window crosses the phone breakpoint, because the map is laid out differently.
let lastNarrow = null;
window.addEventListener("resize", () => {
  const n = window.innerWidth < 760;
  if (lastNarrow !== null && n !== lastNarrow && active?.root.isConnected) window.MUR?.render?.();
});

function s(tag, attrs, ...kids) {
  const el = document.createElementNS(SVGNS, tag);
  for (const [k, v] of Object.entries(attrs || {})) if (v != null && v !== false) el.setAttribute(k, v);
  for (const k of kids.flat()) if (k != null && k !== false) el.append(k);
  return el;
}
const clock = (t) => new Date(t).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
const rgb = (hex) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16)).join(",");
// Index of the first item whose time is greater than t (so: how many are at or before t).
function upper(arr, t, key) {
  let lo = 0, hi = arr.length;
  while (lo < hi) { const mid = (lo + hi) >> 1; if ((key != null ? arr[mid][key] : arr[mid]) <= t) lo = mid + 1; else hi = mid; }
  return lo;
}
function dur(ms) {
  const m = Math.round(ms / MIN);
  if (m < 1) return "now";
  if (m < 60) return m + " min ago";
  const hr = Math.floor(m / 60), r = m % 60;
  return hr + "h" + (r ? " " + r + "m" : "") + " ago";
}
const devLabel = (id) => String(id).replace(/-[0-9a-f]{4}$/, "");
const ease = (p) => (p < 0.5 ? 4 * p * p * p : 1 - Math.pow(-2 * p + 2, 3) / 2);

function build(mur) {
  const { h, avatar, plural, devices, messages, swarm, swStreamOf, fromName, toName, when, streamColor } = mur;
  const ui = mur.ui;
  const winKey = WINS.some((w) => w[0] === ui.rpWin) ? ui.rpWin : "3h";
  const t1 = Date.now();
  const t0 = winKey === "today" ? new Date(t1).setHours(0, 0, 0, 0) : t1 - WINS.find((w) => w[0] === winKey)[2];
  const speed = SPEEDS.includes(ui.rpSpeed) ? ui.rpSpeed : DEF_SPEED[winKey];
  const reduced = REDUCED.matches;
  const narrow = window.innerWidth < 760;
  lastNarrow = narrow;
  const at = (m) => m.sortAt ?? when(m.at);

  // ---- Who is on the map: running sessions.
  const devs = devices();
  const nodes = new Map();
  for (const d of devs) {
    for (const se of d.sessions) {
      nodes.set(se.name, { name: se.name, d, se, stream: swStreamOf(se.name) || null, partners: new Set(), acts: [], sent: 0, got: 0, startAt: null, offAt: null });
    }
  }
  const H = mur.history();
  const hev = (H?.events || []).map((e) => ({ ...e, t: when(e.at) })).filter((e) => e.t != null && nodes.has(e.who));
  for (const nd of nodes.values()) {
    const st = hev.find((e) => e.who === nd.name && e.kind === "started");
    nd.startAt = st ? st.t : when(nd.se.started);
    const off = hev.find((e) => e.who === nd.name && e.kind === "offline");
    nd.offAt = off ? off.t : !nd.d.reachable ? when(nd.d.lastSeen) : null;
  }

  // ---- What happened in the window, oldest first.
  const evs = [];
  const edges = new Map();
  let hidden = 0;
  const inWin = messages.filter((m) => { const x = at(m); return x >= t0 && x <= t1; }).sort((a, b) => at(a) - at(b));
  for (const m of inWin) {
    const t = at(m);
    const from = fromName(m);
    const A = nodes.get(from);
    if (!A) { hidden++; continue; }
    if (m.toKind === "all") {
      evs.push({ t, kind: "bcast", m, from });
      A.acts.push([t, 0]); A.sent++;
    } else if (m.toKind === "device") {
      const d = devs.find((x) => x.id === m.to);
      const targets = d ? d.sessions.map((x) => x.name).filter((n) => n !== from && nodes.has(n)) : [];
      evs.push({ t, kind: "dev", m, from, targets, dev: devLabel(m.to) });
      A.acts.push([t, 0]); A.sent++;
      for (const n of targets) nodes.get(n).acts.push([t, 1]);
    } else if (m.toKind === "session") {
      const to = toName(m);
      const B = nodes.get(to);
      if (!B || B === A) { hidden++; continue; }
      const pair = [from, to].sort();
      const key = pair.join("|");
      let e = edges.get(key);
      if (!e) edges.set(key, (e = { key, a: pair[0], b: pair[1], msgs: [], times: [], dirs: [] }));
      e.msgs.push(m); e.times.push(t); e.dirs.push(from === e.a ? 1 : 0);
      A.partners.add(to); B.partners.add(from);
      A.sent++; B.got++;
      A.acts.push([t, 0]); B.acts.push([t, 1]);
      evs.push({ t, kind: "msg", m, from, to, e });
    }
  }
  for (const e of hev) {
    if (e.t < t0 || e.t > t1) continue;
    if (e.kind === "commit") { evs.push({ t: e.t, kind: "commit", who: e.who, subject: e.subject, branch: e.branch }); nodes.get(e.who).acts.push([e.t, 0]); }
    else if (e.kind === "started") evs.push({ t: e.t, kind: "start", who: e.who });
    else if (e.kind === "offline") evs.push({ t: e.t, kind: "off", who: e.who });
  }
  evs.sort((a, b) => a.t - b.t);
  for (const nd of nodes.values()) nd.acts.sort((a, b) => a[0] - b[0]);
  const edgeList = [...edges.values()];
  for (const e of edgeList) {
    const sa = nodes.get(e.a).stream, sb = nodes.get(e.b).stream;
    e.cross = !sa || !sb || sa.id !== sb.id;
    e.rgb = e.cross ? PURPLE : GREY;
  }
  const msgEvs = evs.filter((e) => e.kind === "msg" || e.kind === "bcast" || e.kind === "dev");
  const commitEvs = evs.filter((e) => e.kind === "commit");
  const msgTimes = msgEvs.map((e) => e.t);
  const commitTimes = commitEvs.map((e) => e.t);
  const maxC = Math.max(1, ...edgeList.map((e) => e.msgs.length));

  // ---- Layout: computed once from the whole window, so nothing moves while it plays.
  // Streams sit on a grid, the busiest in the middle, arranged so the most talk crosses the least
  // distance; agents in no stream share the last cell. Faces sit two to a row, at a quarter and
  // three quarters of the region, so names don't collide in a region of four.
  const cols = narrow ? 2 : 3;
  const CW = narrow ? 176 : 254, GAP = narrow ? 10 : 18, PAD = narrow ? 4 : 8;
  const TITLE = narrow ? 30 : 36, ROWH = narrow ? 60 : 70;
  const deg = (n) => nodes.get(n).partners.size;
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
    for (const e of edgeList) {
      const i = idx.get(e.a), j = idx.get(e.b);
      if (i == null || j == null || i === j) continue;
      const k = Math.min(i, j) + "," + Math.max(i, j);
      links.set(k, (links.get(k) || 0) + e.msgs.length);
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
  const CH = TITLE + maxRows * ROWH + 4;
  const W = cols * CW + (cols - 1) * GAP + PAD * 2;
  const Hh = rows * CH + (rows - 1) * GAP + PAD * 2;
  for (const rg of regions) {
    rg.x = PAD + rg.cell[1] * (CW + GAP);
    rg.y = PAD + rg.cell[0] * (CH + GAP);
    rg.w = CW; rg.h = CH;
    rg.color = rg.stream ? streamColor(rg.stream.id) : "#8a8a8a";
    const n = rg.names.length, k = perRow(n), rowsR = Math.ceil(n / k);
    rg.names.forEach((name, i) => {
      const nd = nodes.get(name);
      const row = Math.floor(i / k), col = i % k;
      const inRow = Math.min(k, n - row * k);
      const spread = inRow === 2 ? 0.5 : 0.31;
      nd.x = rg.x + CW * (0.5 + (col - (inRow - 1) / 2) * spread);
      nd.y = rg.y + TITLE + ((CH - TITLE - 4) * (row + 0.5)) / rowsR - (narrow ? 7 : 8);
      nd.r = (narrow ? 11 : 14) + Math.min(deg(name), 5) * (narrow ? 0.9 : 1.3);
      nd.region = rg;
    });
  }
  for (const e of edgeList) {
    const A = nodes.get(e.a), B = nodes.get(e.b);
    const dx = B.x - A.x, dy = B.y - A.y, len = Math.hypot(dx, dy) || 1;
    const bow = Math.min(34, len * 0.12);
    Object.assign(e, { ax: A.x, ay: A.y, bx: B.x, by: B.y, mx: (A.x + B.x) / 2 - (dy / len) * bow, my: (A.y + B.y) / 2 + (dx / len) * bow });
    e.d = `M${A.x.toFixed(1)} ${A.y.toFixed(1)} Q${e.mx.toFixed(1)} ${e.my.toFixed(1)} ${B.x.toFixed(1)} ${B.y.toFixed(1)}`;
  }

  // ---- Time: the playhead is ui.rpT (ms); null means "now", the end of the window.
  const clampT = (t) => Math.min(t1, Math.max(t0, t));
  let T = ui.rpT == null ? t1 : clampT(ui.rpT);
  let playing = !!ui.rpPlaying && T < t1;
  let dragging = false;
  const atEnd = () => T >= t1 - 500;
  const D = speed * MIN * 0.9; // how long a message is in flight, in swarm time: 0.9 s on screen
  const tauE = { "1h": 5, "3h": 15, today: 45 }[winKey] * MIN; // how slowly a quiet pair cools
  const tauN = D * 0.8; // how fast a face dims after it acts

  // ---- Tooltip
  const tip = h("div", { class: "rp-tip", role: "tooltip" });
  const showTip = (ev, kids) => {
    tip.replaceChildren(...kids.filter(Boolean));
    tip.style.display = "block";
    let x, y;
    if (ev.clientX != null && ev.type !== "focus") { x = ev.clientX; y = ev.clientY; }
    else { const r = ev.currentTarget.getBoundingClientRect(); x = r.left + r.width / 2; y = r.top + r.height / 2; }
    const tw = tip.offsetWidth, th = tip.offsetHeight;
    tip.style.left = Math.max(8, Math.min(window.innerWidth - tw - 8, x + 14)) + "px";
    tip.style.top = (y + 16 + th > window.innerHeight - 8 ? y - th - 12 : y + 16) + "px";
  };
  const hideTip = () => { tip.style.display = "none"; };
  const tipLine = (strong, rest) => h("div", { class: "rp-tip-l" }, h("b", null, strong), rest ? h("span", null, " " + rest) : null);
  const hoverable = (el, content, onActivate) => {
    el.addEventListener("pointerenter", (ev) => showTip(ev, content()));
    el.addEventListener("pointermove", (ev) => showTip(ev, content()));
    el.addEventListener("pointerleave", hideTip);
    el.addEventListener("focus", (ev) => showTip(ev, content()));
    el.addEventListener("blur", hideTip);
    if (onActivate) {
      el.addEventListener("click", (ev) => { ev.stopPropagation(); hideTip(); onActivate(); });
      el.addEventListener("keydown", (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); onActivate(); } });
    }
  };
  const byWhen = () => (atEnd() ? "by now" : "by " + clock(T));

  // ---- The map: regions and lines below, the moving canvas between, faces on top.
  const base = s("svg", { class: "rp-svg", viewBox: `0 0 ${W} ${Hh}`, role: "group", "aria-label": "Map of sessions by stream, with the messages between them" });
  for (const rg of regions) {
    rg.tint = s("rect", { class: "rp-tint", x: rg.x, y: rg.y, width: rg.w, height: rg.h, rx: 16, fill: rg.color, "fill-opacity": 0 });
    const title = narrow && rg.title.length > 22 ? rg.title.slice(0, 21) + "…" : rg.title;
    base.append(
      s("g", { class: "rp-region" + (rg.stream ? "" : " rp-region-alone") },
        s("rect", { class: "rp-bg", x: rg.x, y: rg.y, width: rg.w, height: rg.h, rx: 16 }),
        rg.tint),
    );
    // Titles go on after the lines, so a line never crosses out a stream's name.
    rg.label = s("g", { class: "rp-region" + (rg.stream ? "" : " rp-region-alone") },
      rg.stream ? s("circle", { cx: rg.x + 17, cy: rg.y + (narrow ? 18 : 21), r: 4, fill: rg.color }) : null,
      s("text", { class: "rp-rtitle", x: rg.x + (rg.stream ? 27 : 14), y: rg.y + (narrow ? 22 : 26) }, title),
      !narrow && rg.stream?.pr ? s("text", { class: "rp-rsub", x: rg.x + rg.w - 14, y: rg.y + 26, "text-anchor": "end" }, "#" + rg.stream.pr) : null);
  }
  const latestOn = (list, t) => { const i = upper(list.times || list, t); return i; };
  for (const e of edgeList) {
    e.line = s("path", { class: "rp-line", d: e.d, stroke: e.cross ? "#e496f6" : "#9e9e9e", "stroke-width": 0, "stroke-opacity": 0 });
    const hit = s("path", { class: "rp-hit", d: e.d });
    const g = s("g", { class: "rp-edge", tabindex: 0, role: "button", "aria-label": `${e.a} and ${e.b}: ${plural(e.msgs.length, "message")}` }, e.line, hit);
    e.g = g;
    hoverable(g, () => {
      const n = latestOn(e, T);
      const last = e.msgs[Math.max(0, n - 1)];
      return [
        tipLine(plural(n, "message"), byWhen()),
        h("div", { class: "rp-tip-s" }, e.a + " and " + e.b + (e.cross ? ", across streams" : ", one stream")),
        n ? h("div", { class: "rp-tip-q" }, "Latest: " + (last.subject || "(no subject)")) : h("div", { class: "rp-tip-q" }, "Their first message comes at " + clock(e.times[0]) + "."),
        h("div", { class: "rp-tip-k" }, "Click to read it"),
      ];
    }, () => { const n = latestOn(e, T); mur.swOpenMessage(e.msgs[Math.max(0, n - 1)]); });
    base.append(g);
  }

  for (const rg of regions) base.append(rg.label);
  const canvas = h("canvas", { class: "rp-canvas", "aria-hidden": "true" });
  const top = s("svg", { class: "rp-top", viewBox: `0 0 ${W} ${Hh}`, "aria-hidden": "false" });
  for (const nd of nodes.values()) {
    const face = avatar(nd.name);
    face.style.fontSize = Math.round(nd.r * 0.8) + "px";
    nd.ring = s("circle", { class: "rp-ring", r: nd.r + 3 });
    nd.g = s("g", { class: "rp-node", transform: `translate(${nd.x.toFixed(1)} ${nd.y.toFixed(1)})`, tabindex: 0, role: "button", "aria-label": nd.name },
      nd.ring,
      s("foreignObject", { class: "rp-face", x: -nd.r, y: -nd.r, width: nd.r * 2, height: nd.r * 2 }, face),
      s("text", { class: "rp-name", y: nd.r + (narrow ? 14 : 17), "text-anchor": "middle" }, nd.name));
    const mine = msgEvs.filter((e) => e.from === nd.name || e.to === nd.name || e.targets?.includes(nd.name));
    hoverable(nd.g, () => {
      const i = upper(mine, T, "t");
      const sent = mine.slice(0, i).filter((e) => e.from === nd.name).length;
      const got = i - sent;
      const c = commitEvs.filter((e) => e.who === nd.name && e.t <= T).length;
      const state = nd.startAt > T ? "Not started yet; starts at " + clock(nd.startAt) : nd.offAt != null && nd.offAt <= T ? "Its device went offline at " + clock(nd.offAt) : null;
      return [
        tipLine(nd.name, nd.stream ? "· " + nd.stream.title : "· no stream"),
        h("div", { class: "rp-tip-s" }, `${plural(sent, "message")} sent, ${got} received, ${plural(c, "commit")} ${byWhen()}`),
        state ? h("div", { class: "rp-tip-q" }, state) : null,
        i ? h("div", { class: "rp-tip-k" }, "Click to read its latest message") : null,
      ];
    }, () => { const i = upper(mine, T, "t"); const e = mine[Math.max(0, i - 1)]; if (e) mur.swOpenMessage(e.m); });
    top.append(nd.g);
  }
  const wrap = h("div", { class: "rp-wrap" }, base, canvas, top);

  // ---- Caption: the latest thing that happened, in a sentence.
  const who = (n) => h("b", null, n);
  const evText = (e) => {
    if (e.kind === "msg") return [who(e.from), " → ", who(e.to), ": ", e.m.subject || "(no subject)"];
    if (e.kind === "bcast") return [who(e.from), " → everyone: ", e.m.subject || "(no subject)"];
    if (e.kind === "dev") return [who(e.from), " → everyone on " + e.dev + ": ", e.m.subject || "(no subject)"];
    if (e.kind === "commit") return [who(e.who), " committed “" + e.subject + "”"];
    if (e.kind === "start") return [who(e.who), " started"];
    return [who(e.who), "’s device went offline"];
  };
  const caption = h("button", { class: "rp-caption", type: "button" });
  let capEv = null;
  caption.addEventListener("click", () => { if (capEv?.m) mur.swOpenMessage(capEv.m); });

  // ---- Transport: play, the clock, the scrubber over message density, speed.
  const playBtn = h("button", { class: "rp-play", type: "button" });
  const clockEl = h("b", { class: "rp-clock-t" });
  const relEl = h("span", { class: "rp-clock-r" });
  const nBins = Math.max(1, Math.ceil((t1 - t0) / BIN[winKey]));
  const binMs = (t1 - t0) / nBins;
  const counts = new Array(nBins).fill(0);
  const ccounts = new Array(nBins).fill(0);
  for (const t of msgTimes) counts[Math.min(nBins - 1, Math.floor((t - t0) / binMs))]++;
  for (const t of commitTimes) ccounts[Math.min(nBins - 1, Math.floor((t - t0) / binMs))]++;
  const maxBin = Math.max(1, ...counts);
  const bars = counts.map((c) => h("i", { class: "rp-bar", style: `height:${c ? Math.max(8, (c / maxBin) * 100) : 0}%` }));
  const ticks = commitEvs.map((e) => h("i", { class: "rp-tick", style: `left:${(((e.t - t0) / (t1 - t0)) * 100).toFixed(2)}%;background:${e.who && nodes.get(e.who).stream ? streamColor(nodes.get(e.who).stream.id) : "#8a8a8a"}` }));
  const headEl = h("i", { class: "rp-head" });
  const axis = [];
  const stepH = (t1 - t0) > 8 * 60 * MIN ? (narrow ? 6 : 3) * 60 * MIN : (t1 - t0) > 2 * 60 * MIN ? 60 * MIN : 15 * MIN;
  for (let x = Math.ceil(t0 / stepH) * stepH; x < t1 - stepH * 0.35; x += stepH) {
    if (x - t0 < stepH * 0.35) continue;
    axis.push(h("span", { style: `left:${(((x - t0) / (t1 - t0)) * 100).toFixed(2)}%` }, clock(x)));
  }
  const scrub = h("div", { class: "rp-scrub", role: "slider", tabindex: 0, "aria-label": "Replay time", "aria-valuemin": String(t0), "aria-valuemax": String(t1) },
    h("div", { class: "rp-bars" }, bars),
    h("div", { class: "rp-ticks" }, ticks),
    headEl);
  const scrubBox = h("div", { class: "rp-scrubbox" }, scrub, h("div", { class: "rp-axis" }, h("span", { class: "rp-ax-0" }, clock(t0)), axis, h("span", { class: "rp-ax-1" }, "Now")));
  const tFromX = (x) => { const r = scrub.getBoundingClientRect(); return t0 + Math.min(1, Math.max(0, (x - r.left) / r.width)) * (t1 - t0); };
  const seek = (t) => { T = clampT(t); ui.rpT = atEnd() ? null : T; if (atEnd() && playing) setPlaying(false); paint(); };
  scrub.addEventListener("pointerdown", (ev) => { dragging = true; scrub.setPointerCapture(ev.pointerId); hideTip(); seek(tFromX(ev.clientX)); });
  scrub.addEventListener("pointermove", (ev) => {
    if (dragging) { seek(tFromX(ev.clientX)); return; }
    const t = tFromX(ev.clientX);
    const i = Math.min(nBins - 1, Math.floor((t - t0) / binMs));
    const a = t0 + i * binMs;
    showTip(ev, [tipLine(plural(counts[i], "message"), ccounts[i] ? "· " + plural(ccounts[i], "commit") : null), h("div", { class: "rp-tip-s" }, clock(a) + " to " + clock(a + binMs)), h("div", { class: "rp-tip-k" }, "Click or drag to move the playhead")]);
  });
  const endDrag = () => { dragging = false; };
  scrub.addEventListener("pointerup", endDrag);
  scrub.addEventListener("pointercancel", endDrag);
  scrub.addEventListener("pointerleave", () => { if (!dragging) hideTip(); });
  scrub.addEventListener("keydown", (ev) => {
    const step = { ArrowLeft: -binMs, ArrowRight: binMs, ArrowDown: -binMs, ArrowUp: binMs, PageDown: -binMs * 6, PageUp: binMs * 6 }[ev.key];
    if (step) { ev.preventDefault(); seek(T + step); }
    else if (ev.key === "Home") { ev.preventDefault(); seek(t0); }
    else if (ev.key === "End") { ev.preventDefault(); seek(t1); }
    else if (ev.key === " ") { ev.preventDefault(); toggle(); }
  });

  const speedSeg = mur.fxSeg("Replay speed", SPEEDS.map((v) => [String(v), v + " min/s"]), String(speed), (v) => { ui.rpSpeed = Number(v); mur.render(); });
  speedSeg.classList.add("rp-speed");
  speedSeg.title = "Minutes of the swarm per second of replay";
  const transport = h("div", { class: "rp-transport" }, scrubBox, h("div", { class: "rp-controls" }, playBtn, h("div", { class: "rp-clock" }, clockEl, relEl), speedSeg));

  // ---- Side panel: what happened around the playhead.
  const panelHead = h("h2", null);
  const panelSub = h("p", { class: "rp-sub" });
  const list = h("div", { class: "rp-list" });
  const panel = h("aside", { class: "rp-panel" }, panelHead, panelSub, list);
  const evRow = (e, future) => {
    const dot = e.kind === "commit" ? h("i", { class: "rp-k rp-k-commit", style: `--c:${nodes.get(e.who).stream ? streamColor(nodes.get(e.who).stream.id) : "#8a8a8a"}` })
      : e.kind === "msg" ? h("i", { class: "rp-k " + (e.e.cross ? "rp-k-x" : "rp-k-n") })
      : e.kind === "bcast" || e.kind === "dev" ? h("i", { class: "rp-k rp-k-b" })
      : h("i", { class: "rp-k rp-k-" + e.kind });
    const line1 = e.kind === "msg" ? [e.from, " → ", e.to] : e.kind === "bcast" ? [e.from, " → everyone"] : e.kind === "dev" ? [e.from, " → everyone on " + e.dev]
      : e.kind === "commit" ? [e.who, " committed"] : e.kind === "start" ? [e.who, " started"] : [e.who, "’s device went offline"];
    const line2 = e.m ? e.m.subject || "(no subject)" : e.kind === "commit" ? e.subject : null;
    return h("button", {
      class: "rp-ev" + (future ? " rp-fut" : "") + (e.m ? "" : " rp-ev-seek"), type: "button",
      title: e.m ? "Read this message" : "Move the playhead here",
      onclick: () => (e.m ? mur.swOpenMessage(e.m) : seek(e.t + 1)),
    }, h("span", { class: "rp-ev-t" }, clock(e.t)), dot, h("span", { class: "rp-ev-w" }, line1), line2 ? h("span", { class: "rp-ev-s" }, line2) : null);
  };
  let listKey = "";
  const paintPanel = (idx) => {
    const head = atEnd() ? "Now" : clock(T);
    if (panelHead.textContent !== head) panelHead.textContent = head;
    const key = idx + "|" + playing + "|" + atEnd() + "|" + (playing ? "" : head);
    if (key === listKey) return;
    listKey = key;
    const nm = upper(msgTimes, T), nc = upper(commitTimes, T);
    panelSub.textContent = (atEnd() ? plural(msgTimes.length, "message") : nm + " of " + plural(msgTimes.length, "message")) + " · " + (atEnd() ? plural(commitTimes.length, "commit") : nc + " of " + plural(commitTimes.length, "commit")) + (atEnd() ? " in the window" : " so far");
    const rowsOut = [];
    if (playing || atEnd()) {
      for (const e of evs.slice(Math.max(0, idx - 7), idx)) rowsOut.push(evRow(e, false));
    } else {
      for (const e of evs.slice(Math.max(0, idx - 4), idx)) rowsOut.push(evRow(e, false));
      rowsOut.push(h("div", { class: "rp-mark" }, h("span", null, "Paused at " + clock(T))));
      for (const e of evs.slice(idx, idx + 3)) rowsOut.push(evRow(e, true));
    }
    if (!rowsOut.length) rowsOut.push(h("p", { class: "rp-muted" }, "Nothing happened in this window."));
    list.replaceChildren(...rowsOut);
  };

  // ---- Play / pause
  const syncPlay = () => {
    playBtn.replaceChildren();
    if (playing) { playBtn.append(h("span", { class: "rp-pause-i" }), h("span", { class: "rp-sr" }, "Pause")); playBtn.className = "rp-play"; playBtn.setAttribute("aria-label", "Pause"); }
    else if (atEnd()) { playBtn.append(h("span", { class: "rp-play-i" }), h("span", null, "Replay " + WIN_TEXT[winKey].toLowerCase())); playBtn.className = "rp-play rp-play-big"; playBtn.setAttribute("aria-label", "Replay " + WIN_TEXT[winKey].toLowerCase() + " from the start"); }
    else { playBtn.append(h("span", { class: "rp-play-i" }), h("span", { class: "rp-sr" }, "Play")); playBtn.className = "rp-play"; playBtn.setAttribute("aria-label", "Play"); }
  };
  const setPlaying = (v) => {
    playing = v;
    ui.rpPlaying = v;
    syncPlay();
    listKey = "";
    if (v) start();
  };
  const toggle = () => {
    if (playing) { setPlaying(false); paint(); return; }
    if (atEnd()) { T = t0; ui.rpT = T; }
    setPlaying(true);
  };
  playBtn.addEventListener("click", toggle);

  // ---- Painting one frame for time T.
  const ctx = canvas.getContext("2d");
  let capIdx = -1, binIdx = -1, wasEnd = atEnd();
  const bez = (e, s0) => { const u = 1 - s0; return [u * u * e.ax + 2 * u * s0 * e.mx + s0 * s0 * e.bx, u * u * e.ay + 2 * u * s0 * e.my + s0 * s0 * e.by]; };
  const ring = (x, y, r, rgbS, a, lw) => { if (a <= 0.01) return; ctx.strokeStyle = `rgba(${rgbS},${a})`; ctx.lineWidth = lw; ctx.beginPath(); ctx.arc(x, y, r, 0, Math.PI * 2); ctx.stroke(); };
  const dot = (x, y, r, rgbS, a) => { if (a <= 0.01) return; ctx.fillStyle = `rgba(${rgbS},${a})`; ctx.beginPath(); ctx.arc(x, y, r, 0, Math.PI * 2); ctx.fill(); };
  const particle = (path, p, rgbS) => {
    const sp = ease(Math.min(1, Math.max(0, p)));
    for (let k = 7; k >= 1; k--) {
      const q = ease(Math.min(1, Math.max(0, p - k * 0.028)));
      if (q >= sp) continue;
      const [x, y] = path(q);
      dot(x, y, 2.6 - k * 0.22, rgbS, 0.55 * (1 - k / 8));
    }
    const [x, y] = path(sp);
    dot(x, y, 9, "255,255,255", 0.1);
    dot(x, y, 5.5, "255,255,255", 0.16);
    dot(x, y, 3.4, "255,255,255", 0.95);
  };
  const nodeAct = (nd, t) => {
    let a = 0;
    for (let i = upper(nd.acts, t + D, 0) - 1; i >= 0; i--) {
      const [x, arrive] = nd.acts[i];
      const when2 = x + arrive * D;
      if (when2 > t) continue;
      const dt = t - when2;
      if (dt > tauN * 6 && x + D < t - tauN * 6) break;
      a += Math.exp(-dt / tauN);
    }
    return Math.min(1, a);
  };
  function paint() {
    const t = T;
    // Lines: thicker with every message so far, brighter while the pair is talking.
    for (const e of edgeList) {
      const n = upper(e.times, t);
      let heat = 0;
      for (let i = n - 1; i >= 0; i--) { const dt = t - e.times[i]; if (dt > tauE * 5) break; heat += Math.exp(-dt / tauE); }
      e.n = n; e.heat = Math.min(1, heat);
      const w = n ? (1.5 + (n / maxC) * 5.5).toFixed(2) : "0";
      const op = n ? (0.2 + 0.72 * e.heat).toFixed(2) : "0";
      if (e._w !== w) { e.line.setAttribute("stroke-width", w); e._w = w; }
      if (e._o !== op) { e.line.setAttribute("stroke-opacity", op); e._o = op; }
      const vis = n > 0;
      if (e._v !== vis) { e.g.style.display = vis ? "" : "none"; e._v = vis; }
    }
    // Faces: fade in when they start, grey out when their device goes offline.
    for (const nd of nodes.values()) {
      const off = nd.offAt != null && t >= nd.offAt;
      const p = nd.startAt == null || nd.startAt < t0 ? 1 : Math.min(1, Math.max(0, (t - nd.startAt) / D));
      const op = off ? 0.42 : t < (nd.startAt ?? -Infinity) ? 0.1 : 0.1 + 0.9 * p;
      const a = off ? 0 : nodeAct(nd, t);
      nd.act = a;
      const k = op.toFixed(2) + (off ? "o" : "");
      if (nd._k !== k) { nd.g.style.opacity = op.toFixed(2); nd.g.classList.toggle("rp-off", off); nd._k = k; }
      const ro = (0.18 + 0.82 * a).toFixed(2);
      if (nd._r !== ro) { nd.ring.setAttribute("stroke-opacity", ro); nd._r = ro; }
    }
    for (const rg of regions) {
      if (!rg.stream) continue;
      const a = Math.min(1, rg.names.reduce((s2, n) => s2 + nodes.get(n).act, 0));
      const fo = (0.065 * a).toFixed(3);
      if (rg._f !== fo) { rg.tint.setAttribute("fill-opacity", fo); rg._f = fo; }
    }

    // Canvas
    const cw = wrap.clientWidth, chh = wrap.clientHeight;
    if (cw && chh) {
      const dpr = window.devicePixelRatio || 1;
      const pw = Math.round(cw * dpr), ph = Math.round(chh * dpr);
      if (canvas.width !== pw || canvas.height !== ph) { canvas.width = pw; canvas.height = ph; }
      ctx.setTransform(pw / W, 0, 0, ph / Hh, 0, 0);
      ctx.clearRect(0, 0, W, Hh);
      ctx.globalCompositeOperation = "lighter";
      // Halos on faces that just acted.
      for (const nd of nodes.values()) {
        if (nd.act < 0.03) continue;
        const g = ctx.createRadialGradient(nd.x, nd.y, nd.r, nd.x, nd.y, nd.r + 16);
        g.addColorStop(0, `rgba(255,255,255,${0.2 * nd.act})`);
        g.addColorStop(1, "rgba(255,255,255,0)");
        ctx.fillStyle = g;
        ctx.beginPath(); ctx.arc(nd.x, nd.y, nd.r + 16, 0, Math.PI * 2); ctx.fill();
      }
      // Recent traffic streams along each line, denser the more a pair has talked lately.
      if (!reduced) {
        const phase = t / (D * 1.6);
        for (const e of edgeList) {
          if (!e.n || e.heat < 0.06) continue;
          const k = 1 + Math.floor(e.heat * 4);
          const fwd = e.dirs[e.n - 1];
          for (let i = 0; i < k; i++) {
            let f = (phase + i / k + (e.key.length % 7) / 7) % 1;
            if (!fwd) f = 1 - f;
            const [x, y] = bez(e, f);
            dot(x, y, 1.8, e.rgb, 0.8 * e.heat * Math.sin(Math.PI * f));
          }
        }
      }
      // Events still playing out at t.
      const span = D * 1.9;
      for (let i = upper(evs, t - span, "t"); i < evs.length && evs[i].t <= t; i++) {
        const e = evs[i];
        const p = (t - e.t) / D;
        if (e.kind === "msg") {
          if (p < 1) {
            const fwd = e.from === e.e.a;
            particle((q) => bez(e.e, fwd ? q : 1 - q), p, e.e.rgb);
          }
        } else if (e.kind === "bcast") {
          const A = nodes.get(e.from);
          for (const off of [0, 0.22, 0.44]) {
            const q = (p - off) / 1.4;
            if (q <= 0 || q >= 1) continue;
            ring(A.x, A.y, A.r + 4 + ease(q) * (narrow ? 150 : 230), "220,220,220", 0.5 * Math.pow(1 - q, 1.6), 1.5);
          }
        } else if (e.kind === "dev") {
          const A = nodes.get(e.from);
          for (const n of e.targets) {
            const B = nodes.get(n);
            const path = (q) => { const mx = (A.x + B.x) / 2, my = (A.y + B.y) / 2 - 30; const u = 1 - q; return [u * u * A.x + 2 * u * q * mx + q * q * B.x, u * u * A.y + 2 * u * q * my + q * q * B.y]; };
            if (p < 1) particle(path, p, "200,200,200");
            else if (p < 1.6) { const q = (p - 1) / 0.6; ring(B.x, B.y, B.r + 4 + q * 12, "255,255,255", 0.7 * (1 - q), 2); }
          }
        } else if (e.kind === "commit") {
          const A = nodes.get(e.who);
          const q = p / 1.3;
          if (q < 1) {
            const c = rgb(A.stream ? streamColor(A.stream.id) : "#8a8a8a");
            ring(A.x, A.y, A.r + 4 + ease(q) * 18, c, Math.pow(1 - q, 1.2), 3 * (1 - q) + 0.8);
            ring(A.x, A.y, A.r + 3.5, c, 0.9 * (1 - q), 3);
          }
        }
      }
      ctx.globalCompositeOperation = "source-over";
    }

    // Clock, scrubber, caption, panel.
    if (atEnd() !== wasEnd) { wasEnd = atEnd(); syncPlay(); }
    clockEl.textContent = atEnd() ? "Now" : clock(t);
    relEl.textContent = atEnd() ? "end of the window" : dur(t1 - t);
    const pct = ((t - t0) / (t1 - t0)) * 100;
    headEl.style.left = pct.toFixed(3) + "%";
    scrub.setAttribute("aria-valuenow", String(Math.round(t)));
    scrub.setAttribute("aria-valuetext", atEnd() ? "Now" : clock(t));
    const bi = Math.min(nBins, Math.floor((t - t0) / binMs + 1e-9));
    if (bi !== binIdx) { bars.forEach((b, i) => b.classList.toggle("rp-on", i < bi || atEnd())); binIdx = bi; }
    const idx = upper(evs, t, "t");
    if (idx !== capIdx) {
      capIdx = idx;
      capEv = evs[idx - 1] || null;
      caption.replaceChildren();
      if (capEv) {
        caption.append(h("span", { class: "rp-cap-t" }, clock(capEv.t)), h("span", { class: "rp-cap-x" }, evText(capEv)));
        caption.disabled = !capEv.m;
        caption.title = capEv.m ? "Read this message" : "";
      } else {
        caption.append(h("span", { class: "rp-cap-t" }, clock(t0)), h("span", { class: "rp-cap-x rp-faint" }, "The window opens. Nothing has happened yet."));
        caption.disabled = true;
      }
    }
    paintPanel(idx);
  }

  // ---- The loop. It runs only while playing, and only while this root is on the page.
  const root = h("div", { class: "fx rp" });
  let raf = 0, last = 0, acc = 0, misses = 0;
  const frame = (ts) => {
    raf = 0;
    if (active !== ctl) return;
    if (!root.isConnected) { if (++misses > 3) return; raf = requestAnimationFrame(frame); return; }
    misses = 0;
    const dt = last ? Math.min(100, ts - last) : 0;
    last = ts;
    if (playing && !dragging) {
      if (reduced) {
        acc += dt;
        if (acc >= 1000) { acc -= 1000; T = Math.min(t1, T + speed * MIN); }
      } else T = Math.min(t1, T + (dt / 1000) * speed * MIN);
      // Skip quiet stretches: once the last thing has finished playing out, and nothing happens
      // for more than 3 seconds of screen time, jump to a second before the next event.
      const ni = upper(evs, T, "t");
      const next = evs[ni]?.t ?? t1, prev = evs[ni - 1]?.t ?? -Infinity;
      if (next - T > 3 * speed * MIN && T - prev > D * 1.9) T = Math.min(t1, next - speed * MIN);
      ui.rpT = T;
      if (T >= t1) { ui.rpT = null; setPlaying(false); }
    }
    paint();
    if (playing) raf = requestAnimationFrame(frame);
  };
  function start() { if (!raf) { last = 0; acc = 0; raf = requestAnimationFrame(frame); } }
  const ctl = { root, stop: () => { if (raf) cancelAnimationFrame(raf); raf = 0; } };
  if (active) active.stop();
  active = ctl;
  syncPlay();
  // First frame as soon as the element is on the page, then again on resize.
  queueMicrotask(() => { if (root.isConnected) paint(); else requestAnimationFrame(() => root.isConnected && paint()); });
  if (playing) start();
  if (window.ResizeObserver) {
    const ro = new ResizeObserver(() => { if (!root.isConnected || active !== ctl) { ro.disconnect(); return; } if (!playing) paint(); });
    ro.observe(wrap);
  }

  // ---- Page
  const legend = h("div", { class: "rp-legend" },
    h("span", null, h("i", { class: "rp-sw rp-sw-n" }), "Same stream"),
    h("span", null, h("i", { class: "rp-sw rp-sw-x" }), "Across streams"),
    h("span", null, h("i", { class: "rp-lg-dot" }), "A message in flight"),
    h("span", null, h("i", { class: "rp-lg-ring" }), "Told everyone"),
    h("span", null, h("i", { class: "rp-lg-commit" }), "A commit, in its stream’s colour"),
    h("span", null, h("i", { class: "rp-lg-off" }), "Device offline"),
    h("span", { class: "rp-faint" }, "Thicker line, more messages; brighter, more recent"),
    hidden ? h("span", { class: "rp-faint" }, plural(hidden, "message") + " with sessions that have since stopped aren’t drawn") : null);

  const agents = nodes.size;
  root.append(
    h("header", { class: "fx-head" },
      h("div", null, h("h1", null, "Replay"),
        h("p", null, `${WIN_TEXT[winKey]}, played back: ${plural(msgEvs.length, "message")} and ${plural(commitEvs.length, "commit")} among ${plural(agents, "agent")}.`)),
      mur.fxSeg("Time window", WINS.map((w) => [w[0], w[1]]), winKey, (v) => { ui.rpWin = v; ui.rpT = null; ui.rpPlaying = false; mur.render(); })),
    h("div", { class: "rp-body" },
      h("div", { class: "rp-stage" }, wrap, caption, transport, legend),
      panel),
    tip);
  return root;
}

window.MURVIEWS ||= {};
window.MURVIEWS.replay = (mur) => build(mur);
window.MUR?.render?.();
