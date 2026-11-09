// #183 view: Since you looked. What changed while you were away, ranked the way a standup recap
// is: what needs a person, what the agents settled, what moved in each stream of work, and the
// rest folded behind a count. Everything is computed from the messages, the history and the
// streams, except the few sentences in HUMAN_ASKS below.
const MIN = 60 * 1000;
const LOADED = Date.now();
// The demo's last look is two hours before the page loaded. In the product it is stored per
// person, and moves when they close this tab.
const LAST_LOOK = LOADED - 120 * MIN;

// Questions that only a person can answer, as a small model would summarise them from the
// message they're keyed to. Written by hand here; in the product a model writes them. Each shows
// only while its message exists and has no answer.
const HUMAN_ASKS = [
  {
    from: "copper-wasp",
    subject: "Signing mur.exe",
    what: "copper-wasp needs a code-signing certificate for mur.exe, which no agent can get.",
    why: (m, mur, span) => "Defender flags mur.exe on first run. It asked " + mur.toName(m) + " " + span + " ago, and nobody has answered.",
    short: "copper-wasp needs a signing certificate",
    act: "Get a certificate, or decide with jun that the first Windows zip ships unsigned.",
  },
];

const WINDOWS = [
  ["since", "Since you looked"],
  ["1h", "Last hour"],
  ["6h", "Last 6 hours"],
];

const PHASE = { review: "ready for review", working: "being worked on", waiting: "waiting" };

function clock(t) {
  return new Date(t).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
}

// "47 minutes", "2 hours": how long, in words a person says.
function span(ms) {
  const m = Math.max(1, Math.round(ms / MIN));
  if (m < 90) return m + (m === 1 ? " minute" : " minutes");
  const hr = Math.round(m / 60);
  return hr + (hr === 1 ? " hour" : " hours");
}

function list(names) {
  if (names.length < 2) return names.join("");
  return names.slice(0, -1).join(", ") + " and " + names[names.length - 1];
}

function devName(d) {
  return d.id.replace(/-[0-9a-f]{4}$/, "");
}

function faces(mur, names) {
  const { h, avatar } = mur;
  const uniq = [...new Set(names)].slice(0, 3);
  return h("span", { class: "cu-faces", title: [...new Set(names)].join(", ") }, uniq.map((n) => avatar(n)));
}

window.MURVIEWS ||= {};
window.MURVIEWS.catchup = (mur) => {
  const { h, icon, ui } = mur;
  const win = ui.cuWin || "since";
  const now = Date.now();
  const T0 = win === "1h" ? LOADED - 60 * MIN : win === "6h" ? LOADED - 360 * MIN : LAST_LOOK;

  const head = (sub) =>
    h(
      "header",
      { class: "fx-head cu-head" },
      h("div", null, h("h1", null, "Since you looked"), h("p", null, sub)),
      mur.fxSeg("Time window", WINDOWS, win, (v) => {
        ui.cuWin = v;
        ui.cuMore = ui.cuRest = false;
        mur.render();
      }),
    );
  if (!mur.state) return h("div", { class: "fx cu" }, head("Loading…"));

  // ---- What the page knows ----
  const devs = mur.devices();
  const me = devs.find((d) => d.self)?.owner || "";
  const live = new Map();
  for (const d of devs) for (const s of d.sessions) live.set(s.name, { d, s });
  const H = mur.history() || { events: [], reads: {} };
  const ev = H.events.map((e) => ({ ...e, t: mur.when(e.at) })).sort((a, b) => a.t - b.t);
  const reads = H.reads || {};
  const msgs = [...mur.messages].sort((a, b) => a.sortAt - b.sortAt);
  const inWin = msgs.filter((m) => m.sortAt >= T0);
  const streams = mur.swarm?.streams || [];
  const prs = mur.state.prs || {};
  const whose = (d) => (d.owner === me ? "your" : d.owner + "'s");
  // Whether the one asked has written back to the asker since, in this thread or another.
  const answered = (m) => msgs.some((r) => r.re === m.id || (r.sortAt > m.sortAt && r.toKind === "session" && mur.fromName(r) === mur.toName(m) && mur.toName(r) === mur.fromName(m)));
  const unread = (m) => m.toKind === "session" && live.has(mur.toName(m)) && !reads[m.id];

  // ---- Needs you: what only a person can unblock, most pressing first ----
  const needs = [];

  // A device that's offline while an agent waits on one of its sessions.
  for (const d of devs) {
    if (d.reachable) continue;
    const names = d.sessions.map((s) => s.name);
    const waiting = msgs.filter((m) => unread(m) && names.includes(mur.toName(m)) && live.has(mur.fromName(m)));
    if (!waiting.length) continue;
    const m = waiting[waiting.length - 1];
    const senders = [...new Set(waiting.map((x) => mur.fromName(x)))];
    const off = mur.when(d.lastSeen) || now;
    const dn = devName(d);
    needs.push({
      who: [...senders, mur.toName(m)],
      what: [whose(d)[0].toUpperCase() + whose(d).slice(1) + " ", h("b", null, dn), " has been offline for " + span(now - off) + ", and " + list(senders) + (senders.length > 1 ? " are" : " is") + " waiting on it."],
      why: mur.fromName(m) + " asked " + mur.toName(m) + " “" + m.subject + "” " + span(now - m.sortAt) + " ago. It arrives when " + dn + " is back.",
      act: d.owner === me ? "Turn " + dn + " back on, or tell " + mur.fromName(m) + " to go ahead without it." : "Ask " + d.owner + " to bring " + dn + " back.",
      ev: m,
      short: dn + " is offline",
    });
  }

  // The same command failing again and again. Tool calls are seen on this device only.
  const tools = new Map();
  for (const e of ev) if (e.kind === "tool") tools.set(e.who + "\n" + e.detail, [...(tools.get(e.who + "\n" + e.detail) || []), e]);
  const failing = [];
  for (const [key, runs] of tools) {
    let n = 0;
    for (let i = runs.length - 1; i >= 0 && runs[i].ok === false; i--) n++;
    if (n < 3) continue;
    const [who, cmd] = key.split("\n");
    const first = runs[runs.length - n];
    const last = runs[runs.length - 1];
    const edits = [...new Set(ev.filter((e) => e.who === who && e.kind === "tool" && e.tool === "Edit" && e.t > first.t - 3 * MIN && e.t < last.t).map((e) => e.detail.split("/").pop()))];
    const f = live.get(who);
    failing.push({ who, first });
    needs.push({
      who: [who],
      what: [h("b", null, who), " has run ", h("code", null, cmd), " " + n + " times in a row, and it failed every time."],
      why: "The last run was " + span(now - last.t) + " ago" + (edits.length ? ", after edits to " + list(edits) : "") + ".",
      act: f && f.d.self ? "Open " + who + (/ in (\S+)/.test(f.s.agent) ? " in " + f.s.agent.match(/ in (\S+)/)[1] : "") + " and read the failure before it tries again." : "Ask " + (f?.d.owner || "its owner") + " to look at it.",
      go: ["Timeline", "timeline"],
      short: who + "'s " + cmd.split(" ").slice(0, 2).join(" ") + " keeps failing",
    });
  }

  // A stream in review whose next step, in its agents' words, is a review.
  const ready = streams.filter((s) => s.pr && !s.draft && s.phase === "review" && /review/i.test(s.next || ""));
  for (const s of ready) {
    const url = prs[s.branch]?.url;
    const pr = "#" + s.pr;
    needs.push({
      who: s.who,
      what: ["PR " + pr + ", ", h("b", null, s.title), ", is ready for a person to review."],
      why: "Its next step, as " + list(s.who) + " put it: “" + s.next + "”",
      short: "PR " + pr + " needs a review",
      act: ["Review ", url ? h("a", { class: "cu-link", href: url, target: "_blank", rel: "noreferrer" }, pr + " on GitHub") : pr, "."],
    });
  }

  // Questions only a person can answer (hand-written summaries, keyed to their message).
  for (const a of HUMAN_ASKS) {
    const m = msgs.find((x) => mur.fromName(x) === a.from && x.subject === a.subject);
    if (!m || answered(m)) continue;
    needs.push({ who: [a.from], what: a.what, why: a.why(m, mur, span(now - m.sortAt)), act: a.act, ev: m, short: a.short });
  }

  // One of your sessions nobody can place, with questions to it unanswered.
  for (const u of mur.swarm?.unknown || []) {
    const f = live.get(u.who);
    if (!f || f.d.owner !== me) continue;
    const asked = msgs.filter((m) => m.toKind === "session" && mur.toName(m) === u.who && live.has(mur.fromName(m)) && !answered(m));
    if (!asked.length) continue;
    const askers = [...new Set(asked.map((m) => mur.fromName(m)))];
    needs.push({
      who: [u.who],
      what: [h("b", null, u.who), ", on your " + devName(f.d) + ", hasn't said what it's for."],
      why: list(askers) + " asked it and got no answer. It's on " + (f.s.branch || "no branch") + " with no status, and was last active " + span(now - mur.when(f.s.lastActive)) + " ago.",
      act: "Give it a status, or stop it if its work is done.",
      ev: asked[0],
      short: u.who + " hasn't said what it's for",
    });
  }

  // A question held by do not disturb.
  for (const m of msgs) {
    const f = unread(m) && live.get(mur.toName(m));
    if (!f || !f.d.reachable || !mur.onDnd(f.s) || !live.has(mur.fromName(m))) continue;
    needs.push({
      who: [mur.fromName(m), mur.toName(m)],
      what: [h("b", null, mur.fromName(m)), " has waited " + span(now - m.sortAt) + " for ", h("b", null, mur.toName(m)), ", which is on do not disturb."],
      why: "It asked “" + m.subject + "”.",
      short: mur.fromName(m) + " waits on " + mur.toName(m) + ", on do not disturb",
      act: f.d.owner === me ? "Answer it yourself, or take " + mur.toName(m) + " off do not disturb." : "Ask " + f.d.owner + " whether " + mur.toName(m) + " can answer.",
      ev: m,
    });
  }

  // ---- Decided: what the agents settled in the window (from the Decisions tab's list) ----
  let decisions = null;
  try {
    decisions = typeof window.MURDECISIONS === "function" ? window.MURDECISIONS(mur) || [] : null;
  } catch (e) {
    console.error(e);
  }
  const settled = (decisions || []).filter((d) => !d.open && d.at >= T0).sort((a, b) => b.at - a.at);
  const stillOpen = (decisions || []).filter((d) => d.open);

  // ---- What moved, stream by stream ----
  const commits = ev.filter((e) => e.kind === "commit" && e.t >= T0);
  const startedAt = (n) => mur.when(live.get(n)?.s.started) || 0;
  const rows = [];
  const still = [];
  for (const s of streams) {
    const who = s.who.filter((n) => live.has(n));
    const mine = commits.filter((e) => s.who.includes(e.who));
    const joined = who.filter((n) => startedAt(n) >= T0);
    const isNew = who.length > 0 && joined.length === who.length;
    const offline = ev.filter((e) => e.kind === "offline" && e.t >= T0 && s.who.includes(e.who));
    const fail = failing.filter((f) => s.who.includes(f.who) && f.first.t >= T0 - 30 * MIN);
    const phase = s.tone === "off" ? "offline" : PHASE[s.phase] || s.phase;
    const parts = [];
    if (isNew) parts.push("Started " + span(now - Math.min(...who.map(startedAt))) + " ago, and is now " + phase + ".");
    else if (joined.length) parts.push(list(joined) + (joined.length > 1 ? " joined" : " joined") + ". Now " + phase + ".");
    if (mine.length) {
      const last = mine[mine.length - 1];
      parts.push(plural(mine.length, "commit") + ", the latest “" + last.subject + "”" + (last.pushed === false ? ", not pushed yet." : "."));
    }
    const bad = [];
    for (const o of offline) bad.push("Its device, " + devName(live.get(o.who)?.d || { id: "?" }) + ", went offline at " + clock(o.t) + ".");
    for (const f of fail) bad.push("Tests failing since " + clock(f.first.t) + ".");
    if (!parts.length && !bad.length) {
      still.push(s);
      continue;
    }
    if (!isNew && !joined.length && !offline.length) parts.unshift("Still " + phase + ".");
    rows.push({ s, parts, bad, score: (bad.length ? 100 : 0) + (isNew ? 50 : 0) + mine.length });
  }
  rows.sort((a, b) => b.score - a.score);
  // Agents outside any stream still did work.
  const loose = (mur.swarm?.unknown || [])
    .map((u) => ({ who: u.who, c: commits.filter((e) => e.who === u.who) }))
    .filter((x) => x.c.length);
  const merged = (mur.swarm?.done || []).filter((d) => LOADED - d.at * MIN >= T0);

  // ---- The rest: every message not already cited above ----
  const cited = new Set();
  for (const n of needs) if (n.ev) cited.add(n.ev.id);
  for (const d of settled) for (const id of d.msgs || []) cited.add(id);
  const rest = inWin.filter((m) => !cited.has(m.id));
  const convs = new Map();
  for (const m of rest) {
    const a = mur.fromName(m);
    const b = m.toKind === "session" ? mur.toName(m) : m.toKind === "all" ? "everyone" : m.to;
    const key = m.toKind === "session" ? [a, b].sort().join("|") : a + "|" + b;
    const c = convs.get(key) || { key, a, b, kind: m.toKind, msgs: [] };
    c.msgs.push(m);
    convs.set(key, c);
  }
  const convList = [...convs.values()].sort((x, y) => y.msgs[y.msgs.length - 1].sortAt - x.msgs[x.msgs.length - 1].sortAt);
  const gone = convList.filter((c) => !live.has(c.a) && (c.kind !== "session" || !live.has(c.b)));
  const talk = convList.filter((c) => !gone.includes(c));

  // ---- Header ----
  const agents = new Set(inWin.map((m) => mur.fromName(m)).filter((n) => live.has(n)));
  const fresh = [...live.keys()].filter((n) => startedAt(n) >= T0);
  const sub =
    (win === "since" ? "Since you looked at " + clock(T0) + ", " + span(LOADED - T0) + " ago: " : "Since " + clock(T0) + ": ") +
    plural(inWin.length, "message") +
    " from " +
    plural(agents.size, "agent") +
    ", " +
    plural(commits.length, "commit") +
    (fresh.length ? ", and " + plural(fresh.length, "new agent") : "") +
    ".";

  // ---- Render ----
  const evLink = (m, label) =>
    h("button", { class: "cu-link", onclick: () => mur.swOpenMessage(m) }, label || "Read the message");
  const shown = ui.cuMore ? needs : needs.slice(0, 3);
  const needItem = (n) =>
    h(
      "li",
      { class: "cu-need" },
      faces(mur, n.who),
      h(
        "div",
        { class: "cu-need-body" },
        h("p", { class: "cu-what" }, n.what),
        h(
          "p",
          { class: "cu-why" },
          n.why,
          n.ev && [" ", evLink(n.ev)],
          n.go && [" ", h("button", { class: "cu-link", onclick: () => mur.go(n.go[1]) }, "See it on the " + n.go[0])],
        ),
        h("p", { class: "cu-act" }, icon("arrowRight"), h("span", null, n.act)),
      ),
    );

  const needSec = h(
    "section",
    { class: "cu-sec" },
    h("div", { class: "cu-sec-head" }, h("h2", null, "Needs you"), needs.length > 0 && h("span", { class: "cu-n" }, String(needs.length))),
    needs.length
      ? [
          h("ol", { class: "cu-needs" }, shown.map(needItem)),
          needs.length > 3 && !ui.cuMore
            ? h(
                "button",
                {
                  class: "cu-fold",
                  onclick: () => {
                    ui.cuMore = true;
                    mur.render();
                  },
                },
                icon("chevronRight"),
                needs.length - 3 + " more: " + list(needs.slice(3).map((n) => n.short)) + ".",
              )
            : h("p", { class: "cu-safe" }, icon("check"), "Nothing else needs you."),
        ]
      : h("p", { class: "cu-safe cu-none" }, icon("check"), "Nothing needs you."),
  );

  const decSec =
    decisions &&
    h(
      "section",
      { class: "cu-sec" },
      h("div", { class: "cu-sec-head" }, h("h2", null, "Decided"), settled.length > 0 && h("span", { class: "cu-n" }, String(settled.length))),
      settled.length
        ? h(
            "ul",
            { class: "cu-list" },
            settled.slice(0, 4).map((d) => {
              const ms = (d.msgs || []).map((id) => msgs.find((m) => m.id === id)).filter(Boolean);
              return h(
                "li",
                { class: "cu-item" },
                faces(mur, d.who || []),
                h(
                  "div",
                  { class: "cu-item-body" },
                  h("p", null, d.what),
                  h(
                    "p",
                    { class: "cu-meta" },
                    list(d.who || []) + " · " + clock(d.at),
                    ms.length > 0 && [" · ", evLink(ms[0], plural(ms.length, "message"))],
                  ),
                ),
              );
            }),
          )
        : h("p", { class: "cu-quiet" }, "Nothing was settled since " + clock(T0) + "."),
      h(
        "p",
        { class: "cu-foot" },
        settled.length > 4 ? plural(settled.length - 4, "more decision") + ". " : "",
        stillOpen.length ? plural(stillOpen.length, "question") + " still open. " : "No open questions. ",
        h("button", { class: "cu-link", onclick: () => mur.go("decisions") }, "All decisions"),
      ),
    );

  const moveSec = h(
    "section",
    { class: "cu-sec" },
    h("div", { class: "cu-sec-head" }, h("h2", null, "What moved")),
    rows.length || merged.length || loose.length
      ? h(
          "ul",
          { class: "cu-list" },
          merged.map((d) =>
            h(
              "li",
              { class: "cu-row" },
              h("div", { class: "cu-row-top" }, h("span", { class: "cu-merged" }, icon("check")), h("span", { class: "cu-title" }, d.title), faces(mur, d.who)),
              h("p", { class: "cu-row-text" }, "#" + d.issue + " merged " + span(d.at * MIN) + " ago, by " + list(d.who) + "."),
            ),
          ),
          rows.map(({ s, parts, bad }) =>
            h(
              "li",
              { class: "cu-row" },
              h("div", { class: "cu-row-top" }, mur.fxDot({ ok: "on", warn: "idle", error: "dnd", off: "off" }[s.tone] || "off"), h("span", { class: "cu-title" }, s.title), faces(mur, s.who)),
              h("p", { class: "cu-row-text" }, parts.join(" "), bad.length > 0 && h("span", { class: "cu-bad" }, " " + bad.join(" "))),
            ),
          ),
          loose.map(({ who, c }) =>
            h(
              "li",
              { class: "cu-row" },
              h("div", { class: "cu-row-top" }, mur.fxDot("off"), h("span", { class: "cu-title" }, who + ", in no stream"), faces(mur, [who])),
              h("p", { class: "cu-row-text" }, plural(c.length, "commit") + " on " + (live.get(who)?.s.branch || "its branch") + ", the latest “" + c[c.length - 1].subject + "”."),
            ),
          ),
        )
      : h("p", { class: "cu-quiet" }, "No stream moved since " + clock(T0) + "."),
    still.length > 0 && h("p", { class: "cu-safe" }, icon("check"), "No commits, new agents or trouble: " + list(still.map((s) => s.title)) + "."),
  );

  const restSec = h(
    "section",
    { class: "cu-sec cu-rest" + (ui.cuRest ? " open" : "") },
    h(
      "button",
      {
        class: "cu-fold",
        "aria-expanded": ui.cuRest ? "true" : "false",
        onclick: () => {
          ui.cuRest = !ui.cuRest;
          mur.render();
        },
      },
      icon(ui.cuRest ? "chevronDown" : "chevronRight"),
      rest.length
        ? "And " + plural(rest.length, "other message") + " in " + plural(convList.length, "conversation") + (commits.length ? ", and " + plural(commits.length, "commit") : "")
        : commits.length ? "And " + plural(commits.length, "commit") : "Nothing else",
    ),
    ui.cuRest && [
      talk.length > 0 && [h("h3", { class: "cu-sub" }, "Conversations"), h("ul", { class: "cu-convs" }, talk.map((c) => convRow(mur, c)))],
      gone.length > 0 && [h("h3", { class: "cu-sub" }, "From agents that have stopped"), h("ul", { class: "cu-convs gone" }, gone.map((c) => convRow(mur, c)))],
      commits.length > 0 && [
        h("h3", { class: "cu-sub" }, "Commits"),
        h(
          "ul",
          { class: "cu-commits" },
          [...commits].reverse().map((e) =>
            h(
              "li",
              null,
              mur.avatar(e.who),
              h("span", { class: "cu-commit-text" }, h("span", { class: "cu-commit-subj" }, e.subject), h("span", { class: "cu-commit-who" }, e.who + " on " + e.branch + (e.pushed === false ? ", not pushed" : ""))),
              h("span", { class: "mono cu-sha" }, e.sha || ""),
              h("span", { class: "cu-conv-t" }, clock(e.t)),
            ),
          ),
        ),
      ],
    ],
  );

  return h("div", { class: "fx cu" }, head(sub), needSec, decSec, moveSec, restSec);

  function plural(n, w) {
    return mur.plural(n, w);
  }
};

// One conversation in the fold: who, what it was about, how many messages, when the last was.
function convRow(mur, c) {
  const { h } = mur;
  const last = c.msgs[c.msgs.length - 1];
  const topic = c.kind === "session" ? mur.swarm?.topics?.[[c.a, c.b].sort().join("|")] : null;
  const who = c.kind === "session" ? c.a + " and " + c.b : c.kind === "all" ? c.a + " to everyone" : c.a + " to " + c.b.replace(/-[0-9a-f]{4}$/, "");
  return h(
    "li",
    null,
    h(
      "button",
      { class: "cu-conv", onclick: () => mur.swOpenMessage(last) },
      faces(mur, c.kind === "session" ? [c.a, c.b] : [c.a]),
      h("span", { class: "cu-conv-text" }, h("span", { class: "cu-conv-who" }, who), h("span", { class: "cu-conv-what" }, topic || last.subject.replace(/^Re: /, ""))),
      h("span", { class: "cu-conv-n" }, String(c.msgs.length)),
      h("span", { class: "cu-conv-t" }, clock(last.sortAt)),
    ),
  );
}

window.MUR?.render?.();
