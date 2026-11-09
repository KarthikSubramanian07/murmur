// #183 view: Office. The swarm as a floor plan seen from above: a room per stream of work, a desk
// per agent. Borrowed from Pixel Agents (a session is a character whose animation shows what it is
// doing) and AI Town / Smallville (agents on a map, speech bubbles for what they just said).
window.MURVIEWS ||= {};
window.MURVIEWS.office = (mur) => {
  const { h, avatar, icon, swarm, ago, plural } = mur;
  const ui = mur.ui;
  const NS = "http://www.w3.org/2000/svg";
  const RECENT = 30 * 60000;
  const now = Date.now();

  const lookup = (name) => {
    const f = mur.findSession(name);
    if (!f) return { name, state: "off", session: null, device: null };
    return { name, state: mur.sessionState(f.device, f.session), session: f.session, device: f.device };
  };
  const sent = (name) => (mur.messages || []).filter((m) => m.from === name).sort((a, b) => b.sortAt - a.sortAt);
  const trim = (s, n) => (s.length > n ? s.slice(0, n - 1).trimEnd() + "…" : s);
  const recipient = (m) => (m.toKind === "all" ? "everyone" : mur.toName(m));

  const people = {};
  for (const s of swarm.streams) s.who.forEach((n) => (people[n] ||= lookup(n)));
  swarm.unknown.forEach((u) => (people[u.who] ||= lookup(u.who)));

  const tally = { on: 0, idle: 0, dnd: 0, off: 0 };
  swarm.streams.forEach((s) => s.who.forEach((n) => tally[people[n].state]++));
  const lost = swarm.unknown.length;
  const troubled = swarm.streams.filter((s) => s.tone === "error");
  const parts = [];
  if (tally.on) parts.push(plural(tally.on, "agent") + " working at their desks");
  if (tally.idle) parts.push(tally.idle + " asleep");
  if (tally.dnd) parts.push(tally.dnd + " on do not disturb");
  if (tally.off) parts.push(tally.off + " not answering");
  if (lost) parts.push(lost + " wandering the corridor");
  let caption = parts.join(", ") + ".";
  if (troubled.length) caption += " " + troubled.map((s) => s.title).join(" and ") + (troubled.length > 1 ? " are" : " is") + " in trouble.";

  const sel = ui.ofSel;
  const pick = (name) => { ui.ofSel = ui.ofSel === name ? null : name; mur.render(); };

  function person(name, wander) {
    const p = people[name];
    const st = p.state;
    const s = p.session;
    const last = sent(name)[0];
    const m = last && now - last.sortAt < RECENT ? last : null;
    const bubble = m
      ? h("button", { class: "of-bubble", title: m.subject, onclick: (e) => { e.stopPropagation(); mur.swOpenMessage(m); } },
          h("span", { class: "of-bubble-text" }, trim(m.subject, 50)),
          h("span", { class: "of-bubble-to" }, "→ " + recipient(m)))
      : null;
    const desk = h("div", { class: "of-desk" },
      h("div", { class: "of-screen" }, st === "on" ? h("span", { class: "of-typing" }, h("i"), h("i"), h("i")) : null),
      st === "dnd" ? h("div", { class: "of-sign" }, "Do not disturb") : null,
      st === "off" ? h("span", { class: "of-away" }, "away") : null);
    return h("div", { class: "of-seat of-" + st + (wander ? " of-wander" : "") + (sel === name ? " sel" : ""), "data-who": name },
      h("div", { class: "of-bubble-slot" }, bubble),
      h("button", { class: "of-person", "aria-label": name + (s?.doing ? ": " + s.doing : ""), "aria-pressed": sel === name ? "true" : "false", onclick: () => pick(name) },
        h("span", { class: "of-face" }, st === "off" ? h("span", { class: "of-chair" }) : avatar(name),
          st === "idle" ? h("span", { class: "of-z" }, "z", h("b", null, "z")) : null,
          wander ? h("span", { class: "of-q" }, "?") : null),
        h("span", { class: "of-name" }, name)),
      desk);
  }

  function room(stream) {
    const ref = stream.pr ? "PR #" + stream.pr : stream.issue ? "#" + stream.issue : "";
    return h("section", { class: "of-room of-tone-" + stream.tone + " of-n" + stream.who.length, "aria-label": stream.title },
      h("header", { class: "of-wall" },
        h("span", { class: "of-title" }, stream.title),
        ref ? h("span", { class: "of-ref" }, ref) : null,
        stream.tone === "error" ? h("span", { class: "of-trouble" }, icon("alert"), "In trouble") : null),
      h("div", { class: "of-floor" }, stream.who.map((w) => person(w, false))),
      h("span", { class: "of-door", "aria-hidden": "true" }));
  }

  const corridor = h("section", { class: "of-room of-corridor", "aria-label": "Corridor" },
    h("header", { class: "of-wall" }, h("span", { class: "of-title" }, "Corridor"), h("span", { class: "of-ref" }, "in no stream")),
    h("div", { class: "of-floor" }, swarm.unknown.map((u) => person(u.who, true))),
    h("p", { class: "of-note" }, swarm.unknown.map((u) => u.note).join(" ")));

  // Dashed lines between desks in different rooms that spoke to each other in the last 30 minutes.
  const links = [];
  const seen = new Set();
  for (const n of Object.keys(people)) {
    const m = sent(n)[0];
    if (!m || now - m.sortAt >= RECENT || m.toKind !== "session" || !people[m.to]) continue;
    const key = [n, m.to].sort().join("|");
    if (mur.swStreamOf(n)?.id === mur.swStreamOf(m.to)?.id || seen.has(key)) continue;
    seen.add(key);
    links.push([n, m.to]);
  }

  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("class", "of-links");
  svg.setAttribute("aria-hidden", "true");
  const plan = h("div", { class: "of-plan" }, swarm.streams.map(room), corridor, svg);

  const draw = () => {
    svg.replaceChildren();
    const box = plan.getBoundingClientRect();
    svg.setAttribute("viewBox", `0 0 ${box.width} ${box.height}`);
    for (const [a, b] of links) {
      const ea = plan.querySelector(`[data-who="${a}"] .of-desk`);
      const eb = plan.querySelector(`[data-who="${b}"] .of-desk`);
      if (!ea || !eb) continue;
      const ra = ea.getBoundingClientRect(), rb = eb.getBoundingClientRect();
      const x1 = ra.left + ra.width / 2 - box.left, y1 = ra.top + ra.height / 2 - box.top;
      const x2 = rb.left + rb.width / 2 - box.left, y2 = rb.top + rb.height / 2 - box.top;
      const p = document.createElementNS(NS, "path");
      p.setAttribute("d", `M${x1} ${y1} Q${(x1 + x2) / 2 + (y2 - y1 > Math.abs(x2 - x1) ? 70 : 0)} ${(y1 + y2) / 2 - 18} ${x2} ${y2}`);
      svg.append(p);
    }
  };
  requestAnimationFrame(() => {
    draw();
    if (window.ResizeObserver) new ResizeObserver(draw).observe(plan);
  });

  function card() {
    if (!sel || !people[sel]) return null;
    const p = people[sel];
    const s = p.session;
    const last = sent(sel)[0];
    const stateText = { on: "Working", idle: "Asleep: nothing for a while", dnd: "On do not disturb", off: "Not answering: its device is unreachable" }[p.state];
    const row = (k, v) => (v ? h("div", { class: "of-row" }, h("dt", null, k), h("dd", null, v)) : null);
    return h("aside", { class: "of-card", role: "dialog", "aria-label": sel },
      h("button", { class: "of-x", "aria-label": "Close", onclick: () => { ui.ofSel = null; mur.render(); } }, icon("x")),
      h("div", { class: "of-card-head" }, avatar(sel), h("div", null, h("h2", null, sel), h("p", null, stateText))),
      h("dl", null,
        row("Stream", mur.swStreamOf(sel)?.title || "None. Not in any stream of work."),
        row("Doing", s?.doing || "No status set"),
        row("Branch", s?.branch ? h("code", null, s.branch) : null),
        row("Latest message", last
          ? h("button", { class: "of-msg", onclick: () => mur.swOpenMessage(last) }, h("b", null, trim(last.subject, 70)), h("span", null, "→ " + recipient(last) + " · " + ago(last.sortAt)))
          : "Hasn't sent anything")),
      s ? h("button", { class: "of-open", onclick: () => { ui.filter = s.addr; ui.conversation = null; mur.go("messages"); } }, icon("message"), "Open its conversations") : null);
  }

  return h("div", { class: "fx of" },
    h("header", { class: "fx-head" }, h("div", null, h("h1", null, "Office"), h("p", null, caption))),
    plan,
    h("p", { class: "fx-faint of-hint" }, "Click an agent to see what it is doing, or a speech bubble to read the message. Dashed lines join desks in different rooms that spoke in the last half hour."),
    card());
};
window.MUR?.render?.();
