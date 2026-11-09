// #183 view: Code map. The repo as nested circles (d3.pack): packages hold their files, a file's
// size is how much work touches it, and the agents sit in the files they are working in.
// Ideas borrowed from: Gource (a repo as a tree with people beaming at the files they touch, here
// as faces joined to their files by thin lines), code_swarm (Ogawa, 2008: the repo as a swarm of
// people around files), CodeCity (Wettel: size says how big a part of the code is), FASTDash
// (Microsoft Research, 2007: who is editing which file, shown to the whole team) and Palantir
// (Sarma and van der Hoek: early warning when two people edit the same file).
// A file two different streams of work are in is the collision: it gets the warning colour, a
// triangle badge and a label. Two agents of one stream in a file is normal and isn't flagged.
// Files come from mur.swarm.areas; edits and commits come from the history, which only has tool
// calls for this device's agents.
const SVGNS = "http://www.w3.org/2000/svg";
const WARN = "#ffce2f";
const NOSTREAM = "_none";

function s(tag, attrs, ...kids) {
  const el = document.createElementNS(SVGNS, tag);
  for (const [k, v] of Object.entries(attrs || {})) if (v != null && v !== false) el.setAttribute(k, v);
  for (const k of kids.flat()) if (k != null && k !== false) el.append(k);
  return el;
}

// The pack is laid out differently on a phone, so rerender when the window crosses that line.
let lastNarrow = null;
window.addEventListener("resize", () => {
  const n = window.innerWidth < 760;
  if (lastNarrow !== null && n !== lastNarrow) window.MUR?.render?.();
});

const esc = (t) => t.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
const base = (p) => p.split("/").pop();
const list = (xs) => (xs.length < 2 ? xs.join("") : xs.slice(0, -1).join(", ") + " and " + xs[xs.length - 1]);

function build(mur) {
  const { h, avatar, icon, plural, ago, messages, swarm, swStreamOf, findSession, sessionState, fromName, toName, streamColor, swOpenMessage } = mur;
  const ui = mur.ui;
  const d3 = window.d3;
  const narrow = window.innerWidth < 760;
  lastNarrow = narrow;
  const sel = ui.cmSel || null;
  const stream = (who) => swStreamOf(who) || null;
  const skey = (who) => stream(who)?.id || NOSTREAM;
  const scolor = (who) => streamColor(stream(who)?.id);
  const stitle = (who) => stream(who)?.title || "no stream";
  const at = (m) => m.sortAt ?? Date.parse(m.at);

  // Tool calls (this device's agents only), by file path.
  const H = mur.history();
  const tools = (H?.events || []).filter((e) => e.kind === "tool" && e.detail);
  const commits = (H?.events || []).filter((e) => e.kind === "commit");

  // Files: each package's files, plus files only the tool calls know about.
  const pkgs = swarm.areas.map((a) => ({ path: a.path, files: a.files.map(([f, who]) => ({ pkg: a.path, name: f, path: a.path + "/" + f, who: [...who], extra: false })) }));
  const fileByPath = new Map();
  for (const p of pkgs) for (const f of p.files) fileByPath.set(f.path, f);
  for (const t of tools) {
    if (!/^(Edit|Read|Write|MultiEdit)$/.test(t.tool) || !/^[\w./-]+\.\w+$/.test(t.detail) || fileByPath.has(t.detail)) continue;
    const p = pkgs.filter((x) => t.detail.startsWith(x.path + "/")).sort((a, b) => b.path.length - a.path.length)[0];
    if (!p) continue;
    const f = { pkg: p.path, name: t.detail.slice(p.path.length + 1), path: t.detail, who: [], extra: true };
    p.files.push(f);
    fileByPath.set(f.path, f);
  }
  const allFiles = [...fileByPath.values()];
  for (const f of allFiles) {
    f.tools = tools.filter((t) => t.detail === f.path || (t.tool === "Bash" && t.detail.includes(f.path)));
    f.streams = [...new Set(f.who.map(skey))];
    f.collision = f.streams.length >= 2;
    f.value = 1 + 2 * f.who.length + Math.min(10, f.tools.length);
  }
  const touched = allFiles.filter((f) => f.who.length);
  const collisions = touched.filter((f) => f.collision).sort((a, b) => b.who.length - a.who.length || a.path.localeCompare(b.path));
  const agentFiles = new Map();
  for (const f of touched) for (const w of f.who) (agentFiles.get(w) || agentFiles.set(w, []).get(w)).push(f);
  const quiet = pkgs.filter((p) => !p.files.some((f) => f.who.length));

  // Layout: pack each package's files as circles, then lay the packages out in shelves (rows),
  // scaled to fill the width, so the map uses its whole box instead of one big round blob.
  const W = narrow ? 390 : 900;
  const root = d3.hierarchy({ children: pkgs.map((p) => ({ pkg: p, children: p.files.length ? p.files.map((f) => ({ file: f, value: f.value })) : [{ none: true, value: 3 }] })) })
    .sum((d) => d.value || 0)
    .sort((a, b) => b.value - a.value);
  d3.pack().size([1000, 1000]).padding((n) => (n.depth === 0 ? 20 : 5))(root);
  const items = root.children;
  const GAP = narrow ? 8 : 14, LAB = 20, PAD = 10;
  const shelves = (k) => {
    let x = 0, y = 0, rowH = 0, row = [];
    const rows = [];
    const flush = () => { if (row.length) { rows.push({ y, h: rowH, items: row }); y += LAB + rowH; } row = []; x = 0; rowH = 0; };
    for (const it of items) {
      const d = it.r * 2 * k;
      if (x > 0 && x + d > W - 2 * PAD) flush();
      row.push({ it, x: x + d / 2 }); x += d + GAP; rowH = Math.max(rowH, d);
    }
    flush();
    return { rows, h: y + PAD };
  };
  const maxH = narrow ? 640 : 600;
  let lo = 0.05, hi = 3;
  for (let n = 0; n < 24; n++) { const k = (lo + hi) / 2; if (shelves(k).h <= maxH) lo = k; else hi = k; }
  const K = lo;
  const lay = shelves(K);
  const Hh = Math.ceil(lay.h);
  for (const row of lay.rows) {
    const last = row.items[row.items.length - 1];
    const left = (W - (last.x + last.it.r * K)) / 2;
    for (const { it, x } of row.items) { it.cx = x + left; it.cy = row.y + LAB + row.h / 2; }
  }

  // Agent faces sit in their file; an agent in several files is joined to each by a thread.
  const faceSize = (r, n) => Math.max(11, Math.min(22, r * (n === 1 ? 1.15 : n === 2 ? 0.9 : n === 3 ? 0.8 : 0.66)));
  const faces = [];
  for (const pn of items) for (const leaf of pn.leaves()) {
    const f = leaf.data.file;
    if (!f) continue;
    f.x = pn.cx + (leaf.x - pn.x) * K; f.y = pn.cy + (leaf.y - pn.y) * K; f.r = leaf.r * K;
    const n = f.who.length;
    const fs = faceSize(f.r, n);
    f.who.forEach((who, i) => {
      let x = f.x, y = f.y;
      if (n > 1) {
        const a = -Math.PI / 2 + (i / n) * Math.PI * 2 + (n === 2 ? Math.PI / 2 : 0);
        const rho = Math.max(0, f.r - fs / 2 - 1.5);
        x += Math.cos(a) * rho; y += Math.sin(a) * rho;
      }
      faces.push({ who, file: f, x, y, size: fs });
    });
  }

  // Messages that mention a file or its package. A package counts only as a path ("internal/hook"):
  // a one-word package like "docs" is an ordinary word, and matched 25 unrelated messages.
  const mentions = (f) => {
    const re = new RegExp("(^|[^\\w])" + esc(base(f.path)) + "(?![\\w])", "i");
    const pkg = f.pkg.includes("/") ? f.pkg.toLowerCase() : null;
    return messages.filter((m) => {
      const t = (m.subject || "") + " " + (m.body || "");
      return t.toLowerCase().includes(f.path.toLowerCase()) || re.test(t) || (pkg && t.toLowerCase().includes(pkg));
    }).sort((a, b) => at(b) - at(a));
  };
  // Whether the streams in a collision have talked about it: a message that mentions the file or
  // its package, from one agent in it to another from a different stream.
  const talk = (f) => {
    const ms = mentions(f);
    const pairs = new Map();
    const told = [];
    for (let i = 0; i < f.who.length; i++) for (let j = i + 1; j < f.who.length; j++) {
      if (skey(f.who[i]) !== skey(f.who[j])) pairs.set([f.who[i], f.who[j]].sort().join("|"), { a: f.who[i], b: f.who[j], m: null });
    }
    for (const m of ms) {
      const from = fromName(m);
      if (!f.who.includes(from)) continue;
      let to = [];
      if (m.toKind === "session") to = [toName(m)];
      else if (m.toKind === "device") to = f.who.filter((w) => findSession(w)?.device.id === m.to);
      else if (m.toKind === "all") { told.push(m); continue; }
      for (const r of to) {
        const p = pairs.get([from, r].sort().join("|"));
        if (p && !p.m) p.m = m;
      }
    }
    const all = [...pairs.values()];
    return { talked: all.filter((p) => p.m), silent: all.filter((p) => !p.m), told };
  };

  const who = (a) => a + " (" + stitle(a) + ")";
  const selFile = sel?.startsWith("f:") ? fileByPath.get(sel.slice(2)) : null;
  const selAgent = sel?.startsWith("a:") ? sel.slice(2) : null;
  const pick = (k) => { ui.cmSel = ui.cmSel === k ? null : k; mur.render(); };
  const msgLink = (m, label) => h("button", { class: "cm-link", type: "button", onclick: () => swOpenMessage(m) }, label || "“" + m.subject + "”");
  const msgRow = (m) => h("button", { class: "cm-msg", type: "button", onclick: () => swOpenMessage(m) },
    h("span", { class: "cm-msg-t" }, ago(at(m))), h("span", { class: "cm-msg-w" }, fromName(m) + " to " + (m.toKind === "all" ? "everyone" : toName(m))), h("span", { class: "cm-msg-s" }, m.subject));

  // ---------- SVG ----------
  const dimmed = (kind, o) => {
    if (selFile) return kind === "file" ? o.path !== selFile.path : kind === "face" ? o.file.path !== selFile.path : kind === "thread" ? !selFile.who.includes(o.who) : false;
    if (selAgent) return kind === "file" ? !o.who.includes(selAgent) : kind === "face" ? o.who !== selAgent : kind === "thread" ? o.who !== selAgent : false;
    return false;
  };
  const svg = s("svg", { class: "cm-svg", viewBox: `0 0 ${W} ${Hh}`, role: "img", "aria-label": "Circle packing of the repository: packages hold files, agents sit in the files they work in" });
  const gPkg = s("g"), gFile = s("g"), gThread = s("g"), gFace = s("g"), gLab = s("g"), gBadge = s("g");
  svg.append(gPkg, gFile, gThread, gFace, gBadge, gLab);

  const tipOf = (el, tip) => { el.__tip = tip; el.classList.add("cm-hot"); return el; };

  for (const pn of items) {
    const p = pn.data.pkg;
    const cx = pn.cx, cy = pn.cy, pr = pn.r * K;
    const nA = new Set(p.files.flatMap((f) => f.who)).size;
    const g = s("g", { class: "cm-pkg" + (nA ? "" : " cm-faint") });
    tipOf(g, { title: p.path, lines: nA ? [plural(p.files.filter((f) => f.who.length).length, "file") + " with agents in them", plural(nA, "agent") + " in this package"] : ["Nobody is in this package"] });
    g.append(s("circle", { class: "cm-pkg-c", cx, cy, r: pr }));
    gPkg.append(g);
    // The name sits above the circle; when it is wider than the circle, drop the "internal/" prefix, then hide it.
    const room = pr * 2 + GAP + 6;
    const name = p.path.length * 6.6 <= room ? p.path : p.path.replace(/^internal\//, "");
    if (name.length * 6.6 <= room + 8) gLab.append(s("text", { class: "cm-pkg-l" + (nA ? "" : " cm-faint"), x: cx, y: cy - pr - 5, "text-anchor": "middle" }, name));
  }

  const arc = d3.arc();
  const pie = d3.pie().value((d) => d.n).sort(null);
  for (const f of allFiles) {
    if (f.x == null) continue;
    const g = s("g", { class: "cm-file" + (f.who.length ? "" : " cm-empty") + (f.collision ? " cm-collide" : "") + (selFile?.path === f.path ? " cm-sel" : "") + (dimmed("file", f) ? " cm-dim" : ""), transform: `translate(${f.x},${f.y})`, tabindex: f.who.length ? "0" : null, role: f.who.length ? "button" : null, "aria-label": f.who.length ? f.path + ", " + plural(f.who.length, "agent") + (f.collision ? ", two streams of work are in this file" : "") : null });
    const lines = f.who.length ? [list(f.who.map(who))] : ["No agent is in it; this device's agents have used it"];
    if (f.collision) lines.unshift("Collision: " + f.streams.length + " streams of work are in this file");
    if (f.tools.length) lines.push(plural(f.tools.length, "tool call") + " on this device");
    tipOf(g, { title: f.path, lines, warn: f.collision });
    if (f.who.length) {
      if (f.collision) g.append(s("circle", { class: "cm-halo", r: f.r + 3.5 }));
      const sl = streamsOf(f).map((x) => ({ ...x }));
      if (sl.length === 1) g.append(s("circle", { class: "cm-fill", r: f.r, fill: sl[0].color }));
      else for (const a of pie(sl)) g.append(s("path", { class: "cm-fill", d: arc({ innerRadius: 0, outerRadius: f.r, startAngle: a.startAngle, endAngle: a.endAngle }), fill: a.data.color }));
      g.append(s("circle", { class: "cm-ring", r: f.r, stroke: f.collision ? WARN : sl[0].color }));
      g.addEventListener("click", () => pick("f:" + f.path));
      g.addEventListener("keydown", (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); pick("f:" + f.path); } });
    } else {
      g.append(s("circle", { class: "cm-ring cm-ring-x", r: f.r, stroke: scolorOfTools(f) }));
    }
    gFile.append(g);
    // Label under the circle when it fits, and always on a collision.
    const bn = base(f.path);
    if (f.who.length && (f.collision || selFile?.path === f.path || bn.length * 5.8 <= f.r * 2 + (narrow ? 2 : 16))) gLab.append(s("text", { class: "cm-file-l" + (f.collision ? " cm-file-warn" : "") + (dimmed("file", f) ? " cm-dim" : ""), x: f.x, y: f.y + f.r + 12, "text-anchor": "middle" }, bn));
    if (f.collision) {
      const bx = f.x + f.r * 0.72, by = f.y - f.r * 0.72;
      gBadge.append(s("g", { class: "cm-badge" + (dimmed("file", f) ? " cm-dim" : ""), transform: `translate(${bx},${by})` }, s("path", { d: "M0 -8.5 L8.5 6 L-8.5 6 Z", fill: WARN, stroke: "#1b1b1b", "stroke-width": 1.5, "stroke-linejoin": "round" }), s("text", { x: 0, y: 4.2, "text-anchor": "middle" }, "!")));
    }
  }
  function streamsOf(f) {
    const m = new Map();
    for (const w of f.who) { const k = skey(w); (m.get(k) || m.set(k, { n: 0, color: scolor(w) }).get(k)).n++; }
    return [...m.values()];
  }
  function scolorOfTools(f) {
    const w = f.tools[0]?.who;
    return w ? scolor(w) : "#8a8a8a";
  }

  // Threads from each agent's face to its other files.
  const byAgent = new Map();
  for (const fc of faces) (byAgent.get(fc.who) || byAgent.set(fc.who, []).get(fc.who)).push(fc);
  for (const [name, fcs] of byAgent) {
    if (fcs.length < 2) continue;
    const left = [...fcs];
    let cur = left.shift();
    const order = [cur];
    while (left.length) {
      left.sort((a, b) => Math.hypot(a.x - cur.x, a.y - cur.y) - Math.hypot(b.x - cur.x, b.y - cur.y));
      cur = left.shift();
      order.push(cur);
    }
    for (let i = 1; i < order.length; i++) {
      const a = order[i - 1], b = order[i];
      const mx = (a.x + b.x) / 2, my = (a.y + b.y) / 2;
      const dx = b.x - a.x, dy = b.y - a.y, len = Math.hypot(dx, dy) || 1;
      const k = Math.min(40, len * 0.18);
      const t = s("path", { class: "cm-thread" + (dimmed("thread", { who: name }) ? " cm-dim" : ""), d: `M${a.x},${a.y} Q${mx - (dy / len) * k},${my + (dx / len) * k} ${b.x},${b.y}`, stroke: scolor(name) });
      gThread.append(t);
    }
  }

  for (const fc of faces) {
    const col = scolor(fc.who);
    const fo = s("foreignObject", { x: fc.x - fc.size / 2, y: fc.y - fc.size / 2, width: fc.size, height: fc.size, class: "cm-face-fo" });
    fo.append(h("div", { class: "cm-face" }, avatar(fc.who)));
    fo.style.fontSize = Math.max(8, fc.size * 0.5) + "px";
    const g = s("g", { class: "cm-agent" + (selAgent === fc.who ? " cm-sel" : "") + (dimmed("face", fc) ? " cm-dim" : ""), tabindex: "0", role: "button", "aria-label": fc.who + ", " + stitle(fc.who) });
    const st = findSession(fc.who);
    tipOf(g, { title: fc.who, lines: [stitle(fc.who), st?.session.doing || "", plural(agentFiles.get(fc.who).length, "file")].filter(Boolean) });
    g.append(s("circle", { class: "cm-face-ring", cx: fc.x, cy: fc.y, r: fc.size / 2 + 1.5, stroke: col }), fo);
    g.addEventListener("click", (e) => { e.stopPropagation(); pick("a:" + fc.who); });
    g.addEventListener("keydown", (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); pick("a:" + fc.who); } });
    gFace.append(g);
  }

  // ---------- Tooltip ----------
  const tip = h("div", { class: "cm-tip", role: "tooltip", hidden: true });
  const map = h("div", { class: "cm-map" }, svg, tip);
  const hide = () => { tip.hidden = true; };
  map.addEventListener("pointermove", (e) => {
    const hot = e.target.closest?.(".cm-hot");
    const agent = e.target.closest?.(".cm-agent");
    const el = agent || hot;
    if (!el?.__tip) return hide();
    const t = el.__tip;
    tip.replaceChildren(h("b", { class: t.warn ? "cm-tip-warn" : "" }, t.title), ...t.lines.map((l) => h("span", null, l)));
    tip.hidden = false;
    const r = map.getBoundingClientRect();
    let x = e.clientX - r.left + 14, y = e.clientY - r.top + 14;
    if (x + tip.offsetWidth > r.width - 4) x = e.clientX - r.left - tip.offsetWidth - 14;
    if (y + tip.offsetHeight > r.height - 4) y = e.clientY - r.top - tip.offsetHeight - 14;
    tip.style.transform = `translate(${Math.max(4, x)}px, ${Math.max(4, y)}px)`;
  });
  map.addEventListener("pointerleave", hide);

  // ---------- Legend ----------
  const usedStreams = swarm.streams.filter((st) => touched.some((f) => f.who.some((w) => stream(w)?.id === st.id)));
  const hasNone = touched.some((f) => f.who.some((w) => !stream(w)));
  const legend = h("div", { class: "cm-legend" },
    h("div", { class: "cm-legend-row" },
      usedStreams.map((st) => h("span", { class: "cm-lg" }, h("i", { class: "cm-sw", style: "background:" + streamColor(st.id) }), st.title)),
      hasNone ? h("span", { class: "cm-lg" }, h("i", { class: "cm-sw", style: "background:#8a8a8a" }), "No stream") : null),
    h("div", { class: "cm-legend-row cm-legend-2" },
      h("span", { class: "cm-lg" }, h("i", { class: "cm-sw-warn" }, "!"), "Two streams of work in one file"),
      h("span", { class: "cm-lg" }, h("i", { class: "cm-sw-size" }), "Bigger file, more work on it"),
      h("span", { class: "cm-lg" }, h("i", { class: "cm-sw-faint" }), "Nobody there")));

  // ---------- Panel ----------
  const talkBlock = (f) => {
    const t = talk(f);
    const out = [];
    if (t.talked.length) {
      out.push(h("p", { class: "cm-talk cm-talk-yes" }, icon("message"), h("span", null, list(t.talked.map((p) => p.a + " and " + p.b)) + (t.talked.length > 1 ? " have talked about it: " : " have talked about it: "), t.talked.map((p, i) => [i ? " " : "", msgLink(p.m)]))));
    }
    if (t.silent.length) {
      const names = t.silent.map((p) => "between " + p.a + " and " + p.b);
      const msg = !t.talked.length ? "They haven't talked about it. No message between " + (f.who.length === 2 ? "them" : "these agents") + " mentions " + base(f.path) + " or " + f.pkg + "." : "No message mentions it " + names.join(", or ") + ".";
      out.push(h("p", { class: "cm-talk cm-talk-no" }, icon("alert"), h("span", null, msg)));
    }
    if (t.told.length) out.push(h("p", { class: "cm-talk" }, icon("broadcast"), h("span", null, list([...new Set(t.told.map(fromName))]) + " told everyone: ", msgLink(t.told[0]))));
    return out;
  };
  const collisionSentence = (f) => f.path + ": " + list(f.who.map(who));

  const nFiles = touched.length, nAgents = agentFiles.size;
  let panel;
  if (selFile) {
    const f = selFile;
    const ms = mentions(f);
    const cm = commits.filter((c) => f.who.includes(c.who)).sort((a, b) => Date.parse(b.at) - Date.parse(a.at)).slice(0, 4);
    const edits = f.tools.filter((t) => t.tool === "Edit" || t.tool === "Write" || t.tool === "MultiEdit").length;
    const reads = f.tools.filter((t) => t.tool === "Read").length;
    const last = f.tools.length ? Math.max(...f.tools.map((t) => Date.parse(t.at))) : null;
    panel = h("aside", { class: "cm-panel" },
      h("button", { class: "cm-back", type: "button", onclick: () => pick(sel) }, icon("chevronRight"), "Overview"),
      h("h2", null, base(f.path)),
      h("p", { class: "cm-path" }, f.path),
      f.collision ? h("div", { class: "cm-callout" }, h("p", { class: "cm-callout-h" }, icon("alert"), "Two streams of work are in this file"), talkBlock(f)) : null,
      f.who.length ? h("h3", null, plural(f.who.length, "agent") + " in it") : h("p", { class: "cm-note" }, "No agent is in this file now."),
      h("ul", { class: "cm-list" }, f.who.map((w) => h("li", null, h("button", { type: "button", class: "cm-row", onclick: () => pick("a:" + w) }, avatar(w), h("span", { class: "cm-row-main" }, h("b", null, w), h("span", null, h("i", { class: "cm-sw", style: "background:" + scolor(w) }), stitle(w))), icon("chevronRight"))))),
      f.tools.length ? [h("h3", null, "Edits on this device"), h("p", { class: "cm-note" }, [edits ? plural(edits, "edit") : null, reads ? plural(reads, "read") : null, f.tools.length - edits - reads ? plural(f.tools.length - edits - reads, "command") : null].filter(Boolean).join(", ") + ", the last one " + ago(last) + ".")] : null,
      cm.length ? [h("h3", null, "Latest commits by these agents"), h("ul", { class: "cm-list" }, cm.map((c) => h("li", { class: "cm-commit" }, h("span", { class: "cm-commit-s" }, c.subject), h("span", { class: "cm-commit-m" }, c.who + " on " + c.branch + ", " + ago(Date.parse(c.at))))))] : null,
      h("h3", null, ms.length ? plural(ms.length, "message") + " mention it" : "No message mentions it"),
      ms.length ? h("div", { class: "cm-msgs" }, ms.slice(0, 8).map(msgRow)) : null);
  } else if (selAgent) {
    const fs = agentFiles.get(selAgent) || [];
    const f0 = findSession(selAgent);
    const st = stream(selAgent);
    const state = f0 ? sessionState(f0.device, f0.session) : "off";
    const ac = commits.filter((c) => c.who === selAgent).sort((a, b) => Date.parse(b.at) - Date.parse(a.at)).slice(0, 3);
    panel = h("aside", { class: "cm-panel" },
      h("button", { class: "cm-back", type: "button", onclick: () => pick(sel) }, icon("chevronRight"), "Overview"),
      h("div", { class: "cm-agent-h" }, avatar(selAgent), h("div", null, h("h2", null, selAgent), h("p", { class: "cm-path" }, h("i", { class: "cm-sw", style: "background:" + scolor(selAgent) }), st ? st.title : "In no stream"))),
      f0?.session.doing ? h("p", { class: "cm-note" }, h("span", { class: "fx-dot fx-dot-" + state }), " " + f0.session.doing) : null,
      h("h3", null, fs.length ? "In " + plural(fs.length, "file") : "Not in any file"),
      h("ul", { class: "cm-list" }, fs.map((f) => h("li", null, h("button", { type: "button", class: "cm-row", onclick: () => pick("f:" + f.path) }, f.collision ? h("span", { class: "cm-warn-i", title: "Two streams of work are in this file" }, "!") : h("span", { class: "cm-file-i" }, icon("commit")), h("span", { class: "cm-row-main" }, h("b", { class: "cm-mono" }, base(f.path)), h("span", null, f.pkg + (f.collision ? ", with " + list(f.who.filter((w) => w !== selAgent)) : (f.who.length > 1 ? ", with " + list(f.who.filter((w) => w !== selAgent)) : "")))), icon("chevronRight"))))),
      ac.length ? [h("h3", null, "Latest commits"), h("ul", { class: "cm-list" }, ac.map((c) => h("li", { class: "cm-commit" }, h("span", { class: "cm-commit-s" }, c.subject), h("span", { class: "cm-commit-m" }, c.branch + ", " + ago(Date.parse(c.at))))))] : null);
  } else {
    panel = h("aside", { class: "cm-panel" },
      h("h2", null, plural(nFiles, "file") + " being touched by " + plural(nAgents, "agent")),
      h("p", { class: "cm-note" }, "Across " + plural(pkgs.length - quiet.length, "package") + ". " + (quiet.length ? plural(quiet.length, "package") + " (" + quiet.map((p) => p.path).join(", ") + ") " + (quiet.length === 1 ? "has" : "have") + " nobody in " + (quiet.length === 1 ? "it." : "them.") : "Every package has someone in it.")),
      h("h3", null, collisions.length ? plural(collisions.length, "collision") : "No collisions"),
      collisions.length ? h("ul", { class: "cm-coll" }, collisions.map((f) => h("li", null,
        h("button", { type: "button", class: "cm-coll-b", onclick: () => pick("f:" + f.path) }, h("span", { class: "cm-warn-i" }, "!"), h("span", null, collisionSentence(f))),
        h("div", { class: "cm-coll-t" }, talkBlock(f))))) : h("p", { class: "cm-note" }, "No file has agents from two different streams in it."));
  }

  const subtitle = plural(nFiles, "file") + " in " + plural(pkgs.length, "package") + " with " + plural(nAgents, "agent") + " in them" + (collisions.length ? ", " + plural(collisions.length, "collision") : "");
  return h("div", { class: "fx cm" },
    h("header", { class: "fx-head" }, h("div", null, h("h1", null, "Code map"), h("p", null, subtitle))),
    h("div", { class: "cm-body" }, h("div", { class: "cm-main" }, map, legend), panel));
}

window.MURVIEWS ||= {};
window.MURVIEWS.codemap = (mur) => build(mur);
window.MUR?.render?.();
