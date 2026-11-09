// #183 view: Your agents. The swarm from one person's side: their own agents first, the ones that
// need them on top with what to do, and whose agents theirs talk to. A person runs 3 to 5 agents,
// not 16, and when one of them is stuck on another person's agent, the fix is usually to talk to
// that person, so the picture here is of people rather than agents.
window.MURVIEWS ||= {};
{
  const NS = "http://www.w3.org/2000/svg";
  const MIN = 60000;
  const s = (tag, attrs = {}, ...kids) => {
    const el = document.createElementNS(NS, tag);
    for (const [k, v] of Object.entries(attrs)) if (v != null && v !== false) el.setAttribute(k, v);
    for (const k of kids.flat()) if (k != null && k !== false) el.append(k);
    return el;
  };
  const dur = (ms) => {
    const m = Math.max(1, Math.round(ms / MIN));
    return m < 60 ? m + " min" : Math.floor(m / 60) + " h" + (m % 60 ? " " + (m % 60) + " min" : "");
  };
  const short = (t, n) => (t.length > n ? t.slice(0, n - 1).trimEnd() + "…" : t);
  const deviceLabel = (d) => d.id.replace(/-[0-9a-f]{4}$/, "");
  const subjectOf = (m) => m.subject.replace(/^Re: /, "");
  const pairKey = (a, b) => (a < b ? a + "|" + b : b + "|" + a);

  window.MURVIEWS.mine = (mur) => {
    const { h, icon, presence, avatar, plural, swarm } = mur;
    const ui = mur.ui;
    const now = Date.now();
    const devs = mur.devices();
    const me = devs.find((d) => d.self)?.owner || devs[0]?.owner;
    const people = [];
    for (const d of devs) if (!people.includes(d.owner)) people.push(d.owner);
    const who = people.includes(ui.myPerson) ? ui.myPerson : me;
    const live = new Map();
    for (const d of devs) for (const se of d.sessions) live.set(se.name, { d, s: se, owner: d.owner });
    const ownerOf = (n) => live.get(n)?.owner;
    // "your" always means the person looking at the page, whoever's agents are shown.
    const poss = (n) => (ownerOf(n) === me ? "your" : ownerOf(n) + "'s");
    const Poss = (n) => (ownerOf(n) === me ? "Your" : ownerOf(n) + "'s");
    const t = (m) => m.sortAt ?? mur.when(m.at);

    // Talk between live agents, by pair. Broadcasts and device-wide messages aren't between two.
    const pairs = new Map();
    for (const m of mur.messages || []) {
      if (m.toKind !== "session") continue;
      const a = mur.fromName(m), b = mur.toName(m);
      if (a === b || !live.has(a) || !live.has(b)) continue;
      const k = pairKey(a, b);
      if (!pairs.has(k)) pairs.set(k, []);
      pairs.get(k).push(m);
    }
    for (const ms of pairs.values()) ms.sort((x, y) => t(x) - t(y));
    const topic = (k, ms) => swarm.topics?.[k] || short(subjectOf(ms[ms.length - 1]), 32);

    // A question is waiting when it's the last word between two agents and isn't itself a reply.
    const reads = mur.history()?.reads || {};
    const waits = [];
    for (const ms of pairs.values()) {
      const m = ms[ms.length - 1];
      if (!m.subject.startsWith("Re: ")) waits.push({ from: mur.fromName(m), to: mur.toName(m), m, since: t(m) });
    }

    // What each agent did: its last commit, and a command failing run after run.
    const events = mur.history()?.events || [];
    const lastCommit = new Map(), runs = new Map(), commits = new Map();
    for (const e of events) {
      if (e.kind === "commit") { lastCommit.set(e.who, e); commits.set(e.who, (commits.get(e.who) || 0) + 1); }
      if (e.kind === "tool" && e.tool === "Bash") { if (!runs.has(e.who)) runs.set(e.who, []); runs.get(e.who).push(e); }
    }
    const failing = (n) => {
      const list = runs.get(n) || [];
      const last = list[list.length - 1];
      if (!last || last.ok) return null;
      const same = list.filter((e) => e.detail === last.detail).slice(-3);
      return same.length === 3 && same.every((e) => !e.ok) ? last : null;
    };

    // Why a question hasn't been answered, from the side of the agent that was asked.
    const why = (w) => {
      const L = live.get(w.to), st = mur.sessionState(L.d, L.s);
      if (st === "off") return "Its device has been offline since before it arrived.";
      if (st === "dnd") return w.to + " is on do not disturb, so it hasn't seen it.";
      const r = reads[w.m.id];
      if (r?.how === "inbox") return w.to + " read it " + dur(now - mur.when(r.at)) + " ago and hasn't answered.";
      return null;
    };

    // Everything about one agent that a person may need to act on, most pressing first, and the
    // one thing to do about it.
    const STALE = 10 * MIN;
    const assess = (n) => {
      const { d, s: se } = live.get(n);
      const st = mur.sessionState(d, se);
      const issues = [];
      let act = null;
      const idle = now - mur.when(se.lastActive);
      if (st === "off") {
        issues.push({ text: "Its device, " + deviceLabel(d) + ", has been offline for " + dur(now - mur.when(d.lastSeen)) + "." });
        act ||= "Bring " + deviceLabel(d) + " back online";
      }
      for (const w of waits.filter((x) => x.to === n && now - x.since > STALE).sort((a, b) => a.since - b.since)) {
        const reason = st === "off" ? "" : st === "dnd" ? " It's on do not disturb, so it hasn't seen it." : reads[w.m.id]?.how === "inbox" ? " It read it and hasn't answered." : "";
        issues.push({ text: Poss(w.from) + " " + w.from + " has waited " + dur(now - w.since) + " for its answer to “" + subjectOf(w.m) + "”." + reason, m: w.m, tone: "warn" });
        if (st === "dnd") act ||= "Answer for it, or take it off do not disturb";
      }
      const fail = failing(n);
      if (fail) {
        issues.push({ text: "“" + fail.detail + "” has failed three runs in a row.", tone: "bad" });
        act ||= "Look at why the tests fail";
      }
      if (!se.doing && idle > 15 * MIN) {
        issues.push({ text: "Idle " + dur(idle) + ", with no status" + (commits.get(n) ? "." : " and no commits.") });
        act ||= "Ask it what it's for, or stop it";
      }
      for (const w of waits.filter((x) => x.from === n && now - x.since > STALE).sort((a, b) => a.since - b.since)) {
        const reason = why(w);
        issues.push({ text: "Waiting " + dur(now - w.since) + " on " + poss(w.to) + " " + w.to + " for an answer to “" + subjectOf(w.m) + "”." + (reason ? " " + reason : ""), m: w.m, tone: "warn" });
        const o = ownerOf(w.to);
        act ||= o === who ? "Check on " + w.to : o === me ? "That's your " + w.to + ": check on it" : "Ask " + o + " about " + w.to;
      }
      if (st === "dnd" && !issues.length) issues.push({ text: "On do not disturb." });
      return { n, d, se, st, idle, issues, act, last: lastCommit.get(n) };
    };

    const theirs = [...live.values()].filter((x) => x.owner === who).map((x) => assess(x.s.name));
    const needs = theirs.filter((a) => a.act);
    const fine = theirs.filter((a) => !a.act);
    const devCount = new Set(theirs.map((a) => a.d.id)).size;
    const isMe = who === me;
    const whose = isMe ? "Your" : who + "'s";

    // Talk between people: every message between one person's agents and another's.
    const between = new Map();
    for (const [k, ms] of pairs) {
      const [a, b] = k.split("|");
      const pk = ownerOf(a) === ownerOf(b) ? ownerOf(a) : pairKey(ownerOf(a), ownerOf(b));
      if (!between.has(pk)) between.set(pk, { n: 0, pairs: [] });
      const e = between.get(pk);
      e.n += ms.length;
      e.pairs.push({ k, a, b, ms, topic: topic(k, ms), wait: waits.find((w) => pairKey(w.from, w.to) === k && now - w.since > STALE) });
    }
    const crossOf = (p) => [...between].filter(([k]) => k.includes("|") && k.split("|").includes(p));
    const crossN = crossOf(who).reduce((x, [, e]) => x + e.n, 0);

    const pick = (p) => { ui.myPerson = p; mur.render(); };
    const head = h("header", { class: "fx-head" },
      h("div", null,
        h("h1", null, isMe ? "Your agents" : who + "'s agents"),
        h("p", null, plural(theirs.length, "agent") + " on " + plural(devCount, "device") + " · " + (needs.length ? needs.length + " need " + (isMe ? "you" : who) : "nothing needs " + (isMe ? "you" : who)) + " · " + plural(crossN, "message") + " with other people's agents"),
      ),
      mur.fxSeg("Whose agents", people.map((p) => [p, p === me ? "You" : p, theirs.length && p === who ? null : null]), who, pick),
    );

    // One agent that needs its person.
    const needCard = (a) => h("article", { class: "my-need" },
      h("div", { class: "my-who" },
        presence(a.n, a.d),
        h("div", { class: "my-id" },
          h("div", { class: "my-name" }, a.n, h("span", { class: "my-where" }, (mur.swStreamOf(a.n)?.title || "no stream") + " · " + deviceLabel(a.d))),
          h("div", { class: "my-doing" }, a.se.doing || "No status set"),
        ),
      ),
      h("ul", { class: "my-issues" }, a.issues.map((i) => h("li", { class: "my-issue" + (i.tone ? " my-" + i.tone : "") },
        i.m ? h("button", { class: "my-ev", onclick: () => mur.swOpenMessage(i.m), title: "Open the conversation" }, h("span", null, i.text), icon("chevronRight")) : h("span", null, i.text)))),
      h("p", { class: "my-act" }, icon("arrowRight"), a.act),
    );
    const stateText = (a) => a.st === "on" ? "active" : a.st === "idle" ? "idle " + dur(a.idle) : a.st === "dnd" ? "do not disturb" : "offline";
    const fineRow = (a) => h("div", { class: "my-fine" },
      presence(a.n, a.d),
      h("div", { class: "my-id" },
        h("div", { class: "my-name" }, a.n, h("span", { class: "my-where" }, (mur.swStreamOf(a.n)?.title || "no stream") + " · " + stateText(a))),
        h("div", { class: "my-doing" }, a.se.doing || "No status set"),
        a.last && h("div", { class: "my-last" }, "Last commit " + mur.ago(mur.when(a.last.at)) + ": " + a.last.subject + (a.last.pushed === false ? " (not pushed)" : "")),
      ),
    );

    const left = h("section", { class: "my-col" },
      h("h2", { class: "my-h2" }, needs.length ? "Need " + (isMe ? "you" : who) : "Nothing needs " + (isMe ? "you" : who), needs.length ? h("span", { class: "my-n" }, needs.length) : null),
      needs.map(needCard),
      fine.length ? h("h2", { class: "my-h2 my-h2-fine" }, needs.length ? "Running fine" : "All running fine", h("span", { class: "my-n" }, fine.length)) : null,
      fine.length ? h("div", { class: "my-fines" }, fine.map(fineRow)) : null,
    );

    // The people picture: the person whose agents these are in the middle, everyone else around.
    // Three others stand at the top and the two bottom corners, so no line runs through a name.
    // On a phone the picture is narrower, so the lines between other people lose their labels
    // (their tooltips keep them) and the person in the middle, named in the title, loses theirs.
    const narrow = window.matchMedia("(max-width: 767px)").matches;
    const W = narrow ? 400 : 500, cx = W / 2, cy = 170, X = narrow ? 150 : 190, Y = narrow ? 120 : 116, Yb = narrow ? 92 : 84;
    const others = people.filter((p) => p !== who);
    const pos = new Map([[who, { x: cx, y: cy }]]);
    const spots = others.length === 1 ? [[X, 0]] : others.length === 2 ? [[-X, 0], [X, 0]] : others.length === 3 ? [[0, -Y], [-X, Yb], [X, Yb]]
      : others.map((_, i) => { const r = ((-90 + (360 / others.length) * i) * Math.PI) / 180; return [X * Math.cos(r), Y * Math.sin(r)]; });
    others.forEach((p, i) => pos.set(p, { x: cx + spots[i][0], y: cy + spots[i][1] }));
    const ys = [...pos.values()].map((p) => p.y);
    const top = Math.min(...ys) - (Math.min(...ys) < cy ? 64 : 40), bottom = Math.max(...ys) + 70;
    const cross = [...between].filter(([k]) => k.includes("|"));
    const maxN = Math.max(1, ...cross.map(([, e]) => e.n));
    const svg = s("svg", { class: "my-svg", viewBox: `0 ${top} ${W} ${bottom - top}`, role: "img", "aria-label": "Messages between each person's agents" });
    const pills = [];
    for (const [k, e] of cross.sort((x, y) => x[1].n - y[1].n)) {
      const [p, q] = k.split("|"), A = pos.get(p), B = pos.get(q);
      const mine2 = p === who || q === who;
      const waiting = e.pairs.filter((x) => x.wait).length;
      svg.append(s("line", { class: "my-band" + (mine2 ? " my-band-on" : ""), x1: A.x, y1: A.y, x2: B.x, y2: B.y, "stroke-width": (2 + (e.n / maxN) * 12).toFixed(1) },
        s("title", {}, p + "'s and " + q + "'s agents: " + plural(e.n, "message") + (waiting ? ", " + waiting + " waiting" : ""))));
      // How much, and under it in amber, how many questions are waiting on that line.
      const mx = ((A.x + B.x) / 2).toFixed(1), my = (A.y + B.y) / 2, both = waiting && mine2;
      const pill = (cls, text, y) => {
        const fw = text.length * 7 + 18;
        return s("g", { class: "my-pill" + cls, transform: `translate(${mx} ${y.toFixed(1)})` },
          s("rect", { x: -fw / 2, y: -10, width: fw, height: 20, rx: 10 }), s("text", { "text-anchor": "middle", y: 4 }, text));
      };
      if (mine2 || !narrow) pills.push(pill(mine2 ? "" : " my-pill-off", plural(e.n, "message"), both ? my - 11 : my));
      if (both) pills.push(pill(" my-pill-wait", waiting + " waiting", my + 11));
    }
    svg.append(...pills);
    for (const p of people) {
      const P = pos.get(p), n = [...live.values()].filter((x) => x.owner === p).length, R = p === who ? 30 : 24;
      // A person above the middle has their name above them, out of the way of the lines.
      const up = P.y < cy, ny = up ? P.y - R - 24 : P.y + R + 20, sy = up ? P.y - R - 9 : P.y + R + 36;
      const g = s("g", { class: "my-person" + (p === who ? " my-person-on" : ""), tabindex: 0, role: "button", "aria-label": "Show " + (p === me ? "your" : p + "'s") + " agents" },
        s("circle", { class: "my-ring", cx: P.x, cy: P.y, r: R + 3 }),
        s("foreignObject", { class: "my-face", x: P.x - R, y: P.y - R, width: R * 2, height: R * 2 }, avatar(p)),
        !(narrow && p === who) && s("text", { class: "my-pname", x: P.x, y: ny, "text-anchor": "middle" }, p === me ? p + " (you)" : p),
        !(narrow && p === who) && s("text", { class: "my-psub", x: P.x, y: sy, "text-anchor": "middle" }, plural(n, "agent")),
      );
      g.addEventListener("click", () => pick(p));
      g.addEventListener("keydown", (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); pick(p); } });
      svg.append(g);
    }

    // Under the picture: which agents those messages are between, and about what.
    const pairRow = (x) => {
      const [a, b] = ownerOf(x.a) === who || ownerOf(x.b) !== who ? [x.a, x.b] : [x.b, x.a];
      return h("button", { class: "my-pair" + (x.wait ? " my-pair-wait" : ""), onclick: () => mur.swOpenMessage(x.ms[x.ms.length - 1]), title: "Open the conversation" },
        h("span", { class: "my-pfaces" }, avatar(a), avatar(b)),
        h("span", { class: "my-ptext" },
          h("span", { class: "my-pnames" }, a + " and " + b),
          h("span", { class: "my-ptopic" }, x.wait ? x.wait.from + " waiting " + dur(now - x.wait.since) + ": " + subjectOf(x.wait.m) : x.topic)),
        h("span", { class: "my-pn", title: plural(x.ms.length, "message") }, x.ms.length));
    };
    const groups = crossOf(who).sort((x, y) => y[1].n - x[1].n).map(([k, e]) => {
      const q = k.split("|").find((p) => p !== who);
      return h("div", { class: "my-with" },
        h("h3", null, "With " + (q === me ? "your" : q + "'s") + " agents", h("span", { class: "my-n" }, plural(e.n, "message"))),
        e.pairs.sort((x, y) => !!y.wait - !!x.wait || y.ms.length - x.ms.length).map(pairRow));
    });
    const own = between.get(who);
    if (own) groups.push(h("div", { class: "my-with" },
      h("h3", null, "Among " + (isMe ? "your" : who + "'s") + " own agents", h("span", { class: "my-n" }, plural(own.n, "message"))),
      own.pairs.sort((x, y) => !!y.wait - !!x.wait || y.ms.length - x.ms.length).map(pairRow)));

    const right = h("section", { class: "my-col" },
      h("div", { class: "my-map" },
        h("h2", { class: "my-h2" }, "Whose agents talk to whose"),
        svg),
      groups,
    );

    return h("div", { class: "fx my" }, head, h("div", { class: "my-body" }, left, right));
  };
}
window.MUR?.render?.();
