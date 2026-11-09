// #183 view: Activity. Where the swarm's effort goes over time, and who has gone quiet.
// Borrowed from: the NYT's "Ebb and Flow of Movies" streamgraph (Byron and Wattenberg, 2008:
// stacked layers around a wiggling baseline, each labelled inside its widest part), horizon
// charts and GitHub's pulse graphs (activity as a thin, dense, time-aligned mark), and a
// hospital's heart monitor (a line that goes flat when something stops, one row per patient).
// Both halves share one time axis. Only what every device can see counts: messages sent, commits
// and status changes. Tool calls stay on their own laptop, so counting them would flatter it.
const SVGNS = "http://www.w3.org/2000/svg";
const MIN = 60000;
const WINS = [["1h", "Last hour", "the last hour"], ["3h", "Last 3 hours", "the last 3 hours"], ["day", "Today", "today"]];
const GREY = "#8a8a8a";
const NONE = "_none";
const QUIET_NOTE = 15 * MIN;
const QUIET_SENTENCE = 30 * MIN;
const ALIVE = 6 * MIN;

function s(tag, attrs, ...kids) {
  const el = document.createElementNS(SVGNS, tag);
  for (const [k, v] of Object.entries(attrs || {})) if (v != null && v !== false) el.setAttribute(k, v);
  for (const k of kids.flat()) if (k != null && k !== false) el.append(k);
  return el;
}

const clock = (t) => new Date(t).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
const dur = (ms) => {
  const m = Math.max(1, Math.round(ms / MIN));
  if (m < 60) return m + "m";
  const hh = Math.floor(m / 60), mm = m % 60;
  return mm ? hh + "h " + mm + "m" : hh + "h";
};
const durWords = (ms) => {
  const m = Math.max(1, Math.round(ms / MIN));
  if (m < 60) return m + (m === 1 ? " minute" : " minutes");
  const hh = Math.floor(m / 60), mm = m % 60;
  return hh + (hh === 1 ? " hour" : " hours") + (mm ? " " + mm + " minutes" : "");
};

let lastNarrow = null;
window.addEventListener("resize", () => {
  const n = window.innerWidth < 760;
  if (lastNarrow !== null && n !== lastNarrow) window.MUR?.render?.();
});

function build(mur) {
  const { h, avatar, icon, plural, devices, messages, swarm, swStreamOf, fromName, toName, when, findSession, streamColor, sessionState, fxSeg, swOpenMessage } = mur;
  const ui = mur.ui;
  const narrow = window.innerWidth < 760;
  lastNarrow = narrow;
  const now = Date.now();
  const win = WINS.find((w) => w[0] === ui.puWin) || WINS[1];
  let winMs = win[0] === "1h" ? 60 * MIN : win[0] === "3h" ? 180 * MIN : null;
  if (!winMs) {
    const mid = new Date();
    mid.setHours(0, 0, 0, 0);
    winMs = Math.min(24 * 60 * MIN, Math.max(4 * 60 * MIN, now - mid.getTime()));
  }
  const t0 = now - winMs, t1 = now;
  const span = Math.max(6 * MIN, Math.round(winMs / 18 / MIN) * MIN); // the stretch one hover or click covers
  const H = mur.history();

  const keyOf = (who) => swStreamOf(who)?.id || NONE;
  const titleOf = (key) => (key === NONE ? "No stream" : swarm.streams.find((x) => x.id === key)?.title || key);
  const colorOf = (key) => (key === NONE ? GREY : streamColor(key));

  // Every act the bus carries: messages sent, commits, status changes (tool calls are dropped).
  const acts = [];
  for (const m of messages) {
    const t = m.sortAt ?? when(m.at);
    if (!t || t > now) continue;
    const who = fromName(m);
    acts.push({ t, who, key: keyOf(who), kind: "msg", m });
  }
  for (const e of H?.events || []) {
    if (e.kind !== "commit" && e.kind !== "status" && e.kind !== "tag") continue;
    const t = when(e.at);
    if (!t || t > now) continue;
    acts.push({ t, who: e.who, key: keyOf(e.who), kind: e.kind === "commit" ? "commit" : "status", e });
  }
  acts.sort((a, b) => a.t - b.t);
  const inWin = acts.filter((a) => a.t >= t0);

  // ---- streamgraph data
  const NB = narrow ? 44 : 72;
  const step = winMs / NB;
  const totals = new Map();
  for (const a of inWin) totals.set(a.key, (totals.get(a.key) || 0) + 1);
  const order = [...swarm.streams.map((x) => x.id), NONE];
  const keys = order.filter((k) => totals.get(k));
  const total = inWin.length;
  const raw = new Map(keys.map((k) => [k, new Array(NB).fill(0)]));
  for (const a of inWin) raw.get(a.key)[Math.min(NB - 1, Math.floor((a.t - t0) / step))]++;
  const SIG = 1.4, RAD = 4;
  const kern = [];
  let ksum = 0;
  for (let i = -RAD; i <= RAD; i++) { const w = Math.exp(-(i * i) / (2 * SIG * SIG)); kern.push(w); ksum += w; }
  const smooth = (arr) => arr.map((_, i) => { let v = 0; for (let j = -RAD; j <= RAD; j++) { const k = i + j; if (k >= 0 && k < NB) v += arr[k] * kern[j + RAD]; } return v / ksum; });
  const sm = new Map(keys.map((k) => [k, smooth(raw.get(k))]));
  const rows = [{ t: t0, i: 0 }, ...Array.from({ length: NB }, (_, i) => ({ t: t0 + (i + 0.5) * step, i })), { t: t1, i: NB - 1 }];
  for (const r of rows) for (const k of keys) r[k] = sm.get(k)[r.i];

  const W = narrow ? 350 : 1000, HT = narrow ? 260 : 340, TOP = 8, BOT = 22;
  const x = d3.scaleLinear([t0, t1], [0, W]);
  let layers = [];
  let y = () => 0;
  if (keys.length) {
    layers = d3.stack().keys(keys).value((d, k) => d[k]).offset(d3.stackOffsetWiggle).order(d3.stackOrderInsideOut)(rows);
    const lo = d3.min(layers, (l) => d3.min(l, (p) => p[0])), hi = d3.max(layers, (l) => d3.max(l, (p) => p[1]));
    y = d3.scaleLinear([lo, hi], [HT - BOT - 4, TOP]);
  }
  const area = d3.area().x((p, i) => x(rows[i].t)).y0((p) => y(p[0])).y1((p) => y(p[1])).curve(d3.curveMonotoneX);

  // ---- helpers over the data
  const countsAt = (t) => {
    const c = new Map();
    for (const a of acts) if (a.t >= t0 && Math.abs(a.t - t) <= span / 2) c.set(a.key, (c.get(a.key) || 0) + 1);
    return c;
  };
  const latestMsg = (t) => {
    let best = null;
    for (const a of acts) if (a.kind === "msg" && Math.abs(a.t - t) <= span / 2 && (!best || a.t > best.t)) best = a;
    return best;
  };
  const toLabel = (m) => (m.toKind === "all" ? "everyone" : m.toKind === "device" ? "everyone on " + String(m.to).replace(/-[0-9a-f]{4}$/, "") : toName(m));
  const timeSpan = (t) => clock(Math.max(t0, t - span / 2)) + " to " + clock(Math.min(t1, t + span / 2));

  // ---- tooltip (one per view; hover never re-renders)
  const tip = h("div", { class: "pu-tip", role: "tooltip" });
  const showTip = (nodes, ev) => {
    tip.replaceChildren(...nodes.flat().filter(Boolean));
    tip.classList.add("on");
    const r = tip.getBoundingClientRect();
    let px = ev.clientX + 14, py = ev.clientY + 16;
    if (px + r.width > window.innerWidth - 8) px = ev.clientX - r.width - 14;
    if (px < 8) px = 8;
    if (py + r.height > window.innerHeight - 8) py = ev.clientY - r.height - 12;
    tip.style.left = px + "px";
    tip.style.top = Math.max(8, py) + "px";
  };
  const hideTip = () => tip.classList.remove("on");

  // ---- agents and heartbeats
  const agents = [];
  for (const d of devices()) for (const se of d.sessions) agents.push({ d, se, name: se.name, state: sessionState(d, se) });
  const evs = H?.events || [];
  const latest = (list) => list.map((e) => when(e.at)).filter(Boolean).sort((p, q) => q - p)[0];
  const info = new Map();
  for (const a of agents) {
    const mine = acts.filter((x) => x.who === a.name);
    const offEv = latest(evs.filter((e) => e.who === a.name && e.kind === "offline"));
    const startEv = latest(evs.filter((e) => e.who === a.name && e.kind === "started"));
    const started = startEv ?? when(a.se.started) ?? null;
    const lastAct = mine.length ? mine[mine.length - 1].t : started;
    const offT = a.state === "off" ? offEv ?? when(a.d.lastSeen) ?? now : offEv && offEv >= (lastAct || 0) ? offEv : null;
    const recv = messages
      .filter((m) => (m.toKind === "session" && toName(m) === a.name) || (m.toKind === "device" && m.to === a.d.id))
      .map((m) => ({ t: m.sortAt ?? when(m.at), m }))
      .filter((r) => r.t && r.t <= now && r.t >= t0);
    const quietMs = lastAct ? now - lastAct : null;
    info.set(a.name, { mine: mine.filter((x) => x.t >= t0), recv, started, lastAct, offT, quietMs });
  }
  const quiet = [...info.entries()]
    .map(([name, v]) => ({ name, ...v, silent: v.offT ? now - v.offT : v.quietMs }))
    .filter((v) => v.silent != null && v.silent >= QUIET_SENTENCE)
    .sort((p, q) => q.silent - p.silent);

  // ---- the sentence
  const top = keys.map((k) => [k, totals.get(k)]).sort((p, q) => q[1] - p[1])[0];
  const sentence = [];
  if (top) {
    const share = Math.round((top[1] / total) * 100);
    sentence.push(share > 50 ? "Most of " : "The biggest share of ", win[2], " went to ", h("b", null, titleOf(top[0])), " (" + share + "% of activity)");
  } else sentence.push("Nothing has happened in " + win[2]);
  if (quiet.length) {
    const q = quiet[0];
    const st = swStreamOf(q.name);
    sentence.push("; ", h("b", null, st?.title || q.name), q.offT ? " has been offline for " : " has been silent for ", durWords(q.silent));
    if (quiet.length > 1) sentence.push(", and " + plural(quiet.length - 1, "other agent") + " " + (quiet.length - 1 === 1 ? "has" : "have") + " been quiet for over 30 minutes");
  } else sentence.push("; nobody has been quiet for more than 30 minutes");
  sentence.push(".");

  // ---- the chart
  const svg = s("svg", { class: "pu-svg", viewBox: "0 0 " + W + " " + HT, role: "img", "aria-label": "Streamgraph of activity per stream over " + win[2] });
  const layerEls = new Map();
  const ticks = d3.scaleTime([new Date(t0), new Date(t1)], [0, W]).ticks(narrow ? 4 : 7);
  const gLayers = s("g", null);
  for (const l of layers) {
    const p = s("path", { class: "pu-layer", d: area(l), fill: colorOf(l.key) });
    layerEls.set(l.key, p);
    gLayers.append(p);
  }
  svg.append(gLayers);
  // Direct labels, inside each layer's widest part.
  const gLab = s("g", { class: "pu-labels" });
  for (const l of layers) {
    const th = l.map((p) => Math.abs(y(p[0]) - y(p[1])));
    const best = th.indexOf(Math.max(...th));
    const MINTH = 15;
    if (th[best] < MINTH + 2) continue;
    let a = best, b = best;
    while (a > 0 && th[a - 1] >= MINTH) a--;
    while (b < th.length - 1 && th[b + 1] >= MINTH) b++;
    const x0 = x(rows[a].t), x1 = x(rows[b].t);
    let text = titleOf(l.key);
    const room = x1 - x0 - 10;
    const cw = 6.6;
    if (text.length * cw > room) text = text.slice(0, Math.max(0, Math.floor(room / cw) - 1)).trim() + "…";
    if (text.length < 5) continue;
    const cx = Math.min(Math.max((x0 + x1) / 2, 4 + (text.length * cw) / 2), W - 4 - (text.length * cw) / 2);
    const idx = Math.max(0, rows.findIndex((r) => x(r.t) >= cx));
    const p = l[idx];
    gLab.append(s("text", { class: "pu-lab", x: cx, y: (y(p[0]) + y(p[1])) / 2, "text-anchor": "middle", "dominant-baseline": "central" }, text));
  }
  svg.append(gLab);
  const gAxis = s("g", { class: "pu-axis" });
  for (const t of ticks) {
    gAxis.append(s("line", { x1: x(+t), x2: x(+t), y1: TOP, y2: HT - BOT, class: "pu-grid" }));
    gAxis.append(s("text", { x: x(+t), y: HT - 6, "text-anchor": x(+t) < 24 ? "start" : x(+t) > W - 24 ? "end" : "middle" }, clock(+t)));
  }
  svg.append(gAxis);
  const band = s("rect", { class: "pu-band", y: TOP, height: HT - BOT - TOP, width: 0 });
  const cross = s("line", { class: "pu-cross", y1: TOP, y2: HT - BOT, opacity: 0 });
  const hit = s("rect", { class: "pu-hit", x: 0, y: 0, width: W, height: HT, fill: "transparent" });
  svg.append(band, cross, hit);

  // ---- the selected stretch
  const selBox = h("div", { class: "pu-sel" });
  const hbBand = h("i", { class: "pu-hb-band" });
  const renderSel = () => {
    const t = ui.puSel;
    if (t == null) {
      band.setAttribute("width", 0);
      hbBand.classList.remove("on");
    } else {
      hbBand.classList.add("on");
      const lo = Math.max(t0, t - span / 2), hi = Math.min(t1, t + span / 2);
      band.setAttribute("x", x(lo));
      band.setAttribute("width", x(hi) - x(lo));
      hbBand.style.left = ((lo - t0) / winMs) * 100 + "%";
      hbBand.style.width = ((hi - lo) / winMs) * 100 + "%";
    }
    selBox.replaceChildren();
    selBox.hidden = t == null;
    if (t == null) return;
    const items = acts.filter((a) => a.t >= t0 && Math.abs(a.t - t) <= span / 2 && a.kind !== "status");
    selBox.append(
      h("div", { class: "pu-sel-head" },
        h("div", null, h("b", null, timeSpan(t)), h("span", { class: "fx-faint" }, " · " + plural(items.filter((i) => i.kind === "msg").length, "message") + ", " + plural(items.filter((i) => i.kind === "commit").length, "commit"))),
        h("button", { class: "pu-clear", "aria-label": "Close this list", onclick: () => { ui.puSel = null; renderSel(); } }, icon("x"))),
    );
    if (!items.length) selBox.append(h("p", { class: "fx-faint pu-sel-empty" }, "No messages or commits in this stretch."));
    selBox.append(
      h("ul", { class: "pu-sel-list" }, items.map((a) => {
        const c = colorOf(a.key);
        if (a.kind === "msg") {
          const m = a.m;
          return h("li", null,
            h("button", { class: "pu-row", onclick: () => swOpenMessage(m) },
              h("span", { class: "pu-row-t" }, clock(a.t)), h("span", { class: "pu-bar", style: "background:" + c }), avatar(a.who),
              h("span", { class: "pu-row-main" }, h("span", null, h("b", null, a.who), h("span", { class: "fx-faint" }, " to " + toLabel(m))), h("span", { class: "pu-row-sub" }, m.subject)),
              h("span", { class: "pu-row-go" }, icon("chevronRight"))));
        }
        return h("li", null,
          h("div", { class: "pu-row pu-row-commit" },
            h("span", { class: "pu-row-t" }, clock(a.t)), h("span", { class: "pu-bar", style: "background:" + c }), avatar(a.who),
            h("span", { class: "pu-row-main" }, h("span", null, h("b", null, a.who), h("span", { class: "fx-faint" }, " committed" + (a.e.branch ? " on " + a.e.branch : ""))), h("span", { class: "pu-row-sub" }, a.e.subject || "A commit that isn't pushed yet")),
            h("span", { class: "pu-row-go" }, icon("commit"))));
      })),
    );
  };

  const xToT = (ev) => {
    const r = svg.getBoundingClientRect();
    const fx = Math.min(W, Math.max(0, ((ev.clientX - r.left) / r.width) * W));
    return t0 + (fx / W) * winMs;
  };
  hit.addEventListener("pointermove", (ev) => {
    const t = xToT(ev);
    cross.setAttribute("x1", x(t));
    cross.setAttribute("x2", x(t));
    cross.setAttribute("opacity", 1);
    const c = countsAt(t);
    const lm = latestMsg(t);
    const lines = keys.filter((k) => c.get(k)).sort((p, q) => c.get(q) - c.get(p));
    for (const [k, el] of layerEls) el.classList.toggle("pu-faded", !!c.size && !c.get(k));
    showTip([
      h("div", { class: "pu-tip-h" }, clock(t), h("span", { class: "fx-faint" }, " · " + timeSpan(t))),
      lines.length
        ? lines.map((k) => h("div", { class: "pu-tip-r" }, h("i", { style: "background:" + colorOf(k) }), h("span", null, titleOf(k)), h("b", null, c.get(k))))
        : h("div", { class: "fx-faint" }, "Nothing in this stretch"),
      lm && h("div", { class: "pu-tip-m" }, h("span", { class: "fx-faint" }, "Latest message, " + lm.who + ": "), lm.m.subject),
      h("div", { class: "pu-tip-f" }, "Click to list this stretch"),
    ], ev);
  });
  hit.addEventListener("pointerleave", () => {
    cross.setAttribute("opacity", 0);
    for (const el of layerEls.values()) el.classList.remove("pu-faded");
    hideTip();
  });
  hit.addEventListener("click", (ev) => {
    ui.puSel = xToT(ev);
    renderSel();
    if (ev.pointerType === "touch" || !ev.detail) hideTip();
  });

  // ---- legend with shares
  const legend = h("ul", { class: "pu-legend" }, keys.slice().sort((p, q) => totals.get(q) - totals.get(p)).map((k) =>
    h("li", {
      onmouseenter: () => { for (const [kk, el] of layerEls) el.classList.toggle("pu-faded", kk !== k); },
      onmouseleave: () => { for (const el of layerEls.values()) el.classList.remove("pu-faded"); },
    }, h("i", { style: "background:" + colorOf(k) }), h("span", { class: "pu-leg-n" }, titleOf(k)), h("span", { class: "pu-leg-v" }, Math.round((totals.get(k) / total) * 100) + "%"))));

  // ---- heartbeats
  const pct = (t) => ((Math.min(Math.max(t, t0), t1) - t0) / winMs) * 100;
  const markEl = (cls, t, color, nodesFn, onclick) => {
    const el = h(onclick ? "button" : "span", { class: "pu-mk " + cls, style: "left:" + pct(t) + "%;--c:" + color, onclick: onclick || null });
    el.addEventListener("pointermove", (ev) => showTip(nodesFn(), ev));
    el.addEventListener("pointerleave", hideTip);
    el.append(h("i"));
    return el;
  };
  const msgTip = (verb, t, m, who) => () => [
    h("div", { class: "pu-tip-h" }, clock(t) + " · " + verb),
    h("div", null, h("b", null, who), h("span", { class: "fx-faint" }, " to " + toLabel(m))),
    h("div", { class: "pu-tip-m" }, m.subject),
    h("div", { class: "pu-tip-f" }, "Click to open the conversation"),
  ];
  const trackFor = (a) => {
    const v = info.get(a.name);
    const c = colorOf(keyOf(a.name));
    const track = h("div", { class: "pu-track" });
    const from = Math.max(t0, v.started ?? t0);
    const end = v.offT ?? t1;
    if (!v.offT && v.quietMs >= QUIET_NOTE) {
      const qa = Math.max(t0, v.lastAct);
      track.append(h("span", { class: "pu-quiet", style: "left:" + pct(qa) + "%;width:" + (100 - pct(qa)) + "%" }));
    }
    if (from < end) track.append(h("span", { class: "pu-flat", style: "left:" + pct(from) + "%;width:" + (pct(end) - pct(from)) + "%" }));
    const runs = [];
    for (const m of v.mine) {
      const lo = m.t, hi = Math.min(end, m.t + ALIVE);
      if (runs.length && lo <= runs[runs.length - 1][1]) runs[runs.length - 1][1] = Math.max(runs[runs.length - 1][1], hi);
      else runs.push([lo, hi]);
    }
    for (const [lo, hi] of runs) track.append(h("span", { class: "pu-alive", style: "left:" + pct(lo) + "%;width:" + Math.max(0.3, pct(hi) - pct(lo)) + "%;background:" + c }));
    if (v.started && v.started >= t0) track.append(h("span", { class: "pu-start", style: "left:" + pct(v.started) + "%" }));
    if (v.offT) {
      const oa = Math.max(t0, v.offT);
      track.append(h("span", { class: "pu-off", style: "left:" + pct(oa) + "%;width:" + (100 - pct(oa)) + "%" }, 100 - pct(oa) > 12 ? "offline" : ""));
    }
    for (const r of v.recv) track.append(markEl("pu-in", r.t, "#c8c8c8", msgTip("received", r.t, r.m, fromName(r.m)), () => swOpenMessage(r.m)));
    for (const act of v.mine) {
      if (act.kind === "msg") track.append(markEl("pu-out", act.t, c, msgTip("sent", act.t, act.m, act.who), () => swOpenMessage(act.m)));
      else if (act.kind === "commit") track.append(markEl("pu-commit", act.t, c, () => [h("div", { class: "pu-tip-h" }, clock(act.t) + " · commit"), act.e.branch && h("div", { class: "fx-faint" }, act.e.branch), h("div", { class: "pu-tip-m" }, act.e.subject || "A commit that isn't pushed yet")]));
      else {
        const what = act.e.kind === "tag" ? "Tagged " + act.e.tag : act.e.doing ? "Now: " + act.e.doing : "Cleared its status";
        track.append(markEl("pu-status", act.t, c, () => [h("div", { class: "pu-tip-h" }, clock(act.t) + " · status change"), h("div", { class: "pu-tip-m" }, what)]));
      }
    }
    return track;
  };

  const groups = [...swarm.streams.map((x) => x.id), NONE].map((k) => [k, agents.filter((a) => keyOf(a.name) === k)]).filter(([, l]) => l.length);
  const axis = h("div", { class: "pu-hb-axis" }, h("div", { class: "pu-hb-axis-in" }, ticks.map((t) => h("span", { style: "left:" + pct(+t) + "%" }, clock(+t)))));
  const gridEl = h("div", { class: "pu-hb-grid" }, ticks.map((t) => h("i", { style: "left:" + pct(+t) + "%" })), hbBand);
  const body = h("div", { class: "pu-hb-body" }, gridEl, groups.map(([k, list]) =>
    h("section", { class: "pu-group" },
      h("h3", null, h("i", { style: "background:" + colorOf(k) }), titleOf(k), h("span", { class: "fx-faint" }, plural(list.length, "agent"))),
      list.map((a) => {
        const v = info.get(a.name);
        let note, cls = "";
        if (v.offT) { note = "offline " + dur(now - v.offT); cls = "off"; }
        else if (v.quietMs != null && v.quietMs >= QUIET_NOTE) { note = "quiet " + dur(v.quietMs); cls = "warn"; }
        else note = v.lastAct ? "active " + dur(now - v.lastAct) + " ago" : "";
        return h("div", { class: "pu-hb" },
          h("div", { class: "pu-hb-lab" }, avatar(a.name), h("div", { class: "pu-hb-who" }, h("span", { class: "pu-hb-name" }, a.name), h("span", { class: "pu-hb-note " + cls }, note))),
          trackFor(a));
      }))));

  const root = h("div", { class: "fx pu" },
    h("header", { class: "fx-head" },
      h("div", null, h("h1", null, "Activity"), h("p", null, plural(total, "event") + " from " + plural(agents.length, "agent") + " in " + win[2])),
      fxSeg("Time window", WINS.map(([v, l]) => [v, l]), win[0], (v) => { ui.puWin = v; ui.puSel = null; mur.render(); })),
    h("p", { class: "pu-sentence" }, sentence),
    h("section", { class: "pu-panel" },
      h("div", { class: "pu-chart" },
        h("div", { class: "pu-side" }, legend),
        h("div", { class: "pu-plot" }, svg)),
      h("p", { class: "pu-note" }, icon("alert"), "Counts messages sent, commits and status changes, which every device can see. Tool calls aren't counted: only the laptop running them has them, so they would make its agents look busier than the rest."),
      selBox),
    h("section", { class: "pu-panel pu-hbs" },
      h("div", { class: "pu-hbs-head" },
        h("h2", null, "Heartbeats"),
        h("div", { class: "pu-keys" },
          h("span", null, h("i", { class: "k-out" }), "sent"), h("span", null, h("i", { class: "k-in" }), "received"), h("span", null, h("i", { class: "k-commit" }), "commit"), h("span", null, h("i", { class: "k-status" }), "status"), h("span", null, h("i", { class: "k-quiet" }), "quiet 15m+"))),
      axis, body),
    tip);
  renderSel();
  return root;
}

window.MURVIEWS ||= {};
window.MURVIEWS.pulse = build;
window.MUR?.render?.();
