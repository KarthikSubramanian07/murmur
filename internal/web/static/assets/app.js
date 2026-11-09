// The page. Plain DOM, no framework and no build step: the daemon embeds these files as they
// are. Everything from the bus is untrusted text, so it only ever reaches the page as text nodes.
import { icon } from "./icons.js";

const NAME = document.querySelector('meta[name="app-name"]').content;
const TOKEN_KEY = NAME + ":token";
const POLL_MS = 3000;
const HIDDEN_POLL_MS = 15000;
const MAX_MESSAGES = 2000;
const VIEWS = ["sessions", "messages", "recent", "settings"];
const TITLES = { sessions: "Sessions", messages: "Conversations", recent: "Recent", settings: "Settings" };
const NARROW = window.matchMedia("(max-width: 767px)"); // style.css's mobile width

const ui = {
  view: "messages",
  token: null,
  conn: "connecting", // connecting, ok, down, denied
  state: null,
  messages: [], // oldest first
  ids: new Set(),
  cursor: "",
  settings: null,
  settingsError: "",
  rowErrors: {},
  filter: null, // a session name
  conversation: null, // the open conversation's key
  conversationAnchor: null, // a message in it
  chatListTop: 0,
  muting: new Set(),
  tagging: new Set(), // the sessions whose tags are being changed, by their row key
  tagInput: null, // the row key of the session whose tag field is open
};

class AuthError extends Error {
  constructor(token) {
    super("unauthorized");
    this.token = token;
  }
}

// The token arrives once, in the URL fragment, which never reaches a server or a Referer. It is
// kept for this tab only, and taken out of the address bar at once. The fragment may also name a
// view: #messages&token=...
function takeToken() {
  const parts = location.hash.slice(1).split("&");
  const tok = parts.find((p) => p.startsWith("token="));
  if (tok) {
    let value = tok.slice(6);
    try {
      value = decodeURIComponent(value);
    } catch {}
    sessionStorage.setItem(TOKEN_KEY, value);
    const view = parts.find((p) => VIEWS.includes(p)) || "messages";
    history.replaceState(null, "", location.pathname + location.search + "#" + view);
  }
  return sessionStorage.getItem(TOKEN_KEY);
}

async function api(path, init = {}) {
  const token = ui.token;
  const headers = { Authorization: "Bearer " + token };
  if (init.body) headers["Content-Type"] = "application/json";
  // A tab nobody can see still polls, but mustn't keep the daemon running for good.
  if (document.hidden) headers["X-Page-Hidden"] = "1";
  const res = await fetch(path, { ...init, headers, cache: "no-store" });
  if (res.status === 401) throw new AuthError(token);
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || res.status + " " + res.statusText);
  }
  return res.status === 204 ? null : res.json();
}

// DOM.

function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? "" : String(v));
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid == null || kid === false || kid === "") continue;
    el.append(kid instanceof Node ? kid : String(kid));
  }
  return el;
}

// Time.

function when(s) {
  if (!s) return null;
  const t = Date.parse(s);
  return Number.isNaN(t) || t < Date.UTC(2000, 0, 1) ? null : t;
}

function ago(t) {
  const s = Math.max(0, (Date.now() - t) / 1000);
  if (s < 45) return "now";
  if (s < 3600) return Math.max(1, Math.round(s / 60)) + "m ago";
  if (s < 86400) return Math.floor(s / 3600) + "h ago";
  return Math.floor(s / 86400) + "d ago";
}

function stamp(t) {
  return new Date(t).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
    second: "2-digit",
  });
}

// An element whose text is a relative time, kept current by the ticker.
function agoEl(tag, cls, t, prefix = "") {
  return h(tag, { class: cls, "data-ago": t, "data-prefix": prefix, title: stamp(t) }, prefix + ago(t));
}

function tick() {
  for (const el of document.querySelectorAll("[data-ago]")) {
    el.textContent = el.dataset.prefix + ago(Number(el.dataset.ago));
  }
}

// Lookups over the latest state.

function devices() {
  const list = [...(ui.state?.devices || [])];
  const rank = (d) => (d.self ? 0 : d.reachable ? 1 : 2);
  list.sort((a, b) => rank(a) - rank(b) || a.id.localeCompare(b.id));
  for (const d of list) {
    d.sessions = [...(d.sessions || [])].sort(
      (a, b) => (when(a.started) || 0) - (when(b.started) || 0) || a.name.localeCompare(b.name),
    );
  }
  return list;
}

// The latest name for each address the page has seen. A session works as device/session until its
// name lands, and the page keeps messages from before that, so it goes by the address the bus
// carried and puts in whatever the address is called now.
const named = new Map();

let namesLearned = 0; // goes up whenever an address gets a new name

function learn(addr, name) {
  if (addr && name && name !== addr && named.get(addr) !== name) {
    named.set(addr, name);
    namesLearned++;
  }
}

function nameOf(addr, shown) {
  return (addr && named.get(addr)) || shown;
}

function fromName(m) {
  return nameOf(m.fromAddr, m.from);
}

function toName(m) {
  return m.toKind === "session" ? nameOf(m.toAddr, m.to) : m.to;
}

function findSession(name) {
  for (const d of ui.state?.devices || []) {
    for (const s of d.sessions || []) if (s.name === name) return { device: d, session: s };
  }
  return null;
}

function sessionCount() {
  return (ui.state?.devices || []).reduce((n, d) => n + (d.sessions?.length || 0), 0);
}

function plural(n, word) {
  return n + " " + word + (n === 1 ? "" : "s");
}

// Shell.

const root = document.getElementById("root");
let content; // the scrolling pane, kept across renders so its scroll position is too

function render() {
  if (ui.conn === "denied" || !ui.token) {
    root.replaceChildren(
      h(
        "div",
        { class: "gate" },
        h("p", { class: "gate-title" }, NAME),
        h("p", { class: "gate-line" }, "Open the page again from its link."),
      ),
    );
    content = null;
    return;
  }
  const keep = document.activeElement?.dataset?.key;
  const scroll = content ? content.scrollTop : 0;
  const chat = chatScroll();
  const side = root.querySelector(".sidebar-content, .mini-scroll")?.scrollTop || 0;
  if (!content) content = h("div", { class: "content" });
  const inner = page(); // before the top bar, which counts what this settles
  content.className = "content" + (inner.classList.contains("chat-page") ? " content-chat" : "");
  content.replaceChildren(inner);
  root.replaceChildren(h("div", { class: "app" }, sidebar(), h("main", { class: "main" }, ui.view === "settings" || NARROW.matches ? topbar() : null, content)));
  content.scrollTop = scroll;
  const sideNow = root.querySelector(".sidebar-content, .mini-scroll");
  if (sideNow) sideNow.scrollTop = side;
  restoreChatScroll(chat);
  if (keep) root.querySelector(`[data-key="${CSS.escape(keep)}"]`)?.focus({ preventScroll: true });
}

function navButton(view, iconName, count) {
  return h(
    "li",
    null,
    h(
      "button",
      {
        class: "menu-button",
        "aria-current": ui.view === view ? "page" : null,
        "data-key": "nav-" + view,
        onclick: () => {
          ui.filter = null;
          go(view);
        },
      },
      icon(iconName),
      h("span", { class: "label" }, TITLES[view]),
      count != null && h("span", { class: "menu-count" }, count),
    ),
  );
}

// The sidebar is the sessions themselves, under their devices: every session, every conversation
// and the newest messages at the top, Settings at the foot. It folds to a strip of faces, and
// remembers that.
const MINI_KEY = NAME + ":sidebar-mini";
let mini = false;
try {
  mini = localStorage.getItem(MINI_KEY) === "1";
} catch {}
function setMini(v) {
  mini = v;
  try {
    localStorage.setItem(MINI_KEY, v ? "1" : "0");
  } catch {}
  render();
}

function miniSidebar() {
  const pick = (view) => () => {
    ui.filter = null;
    if (view === "messages") ui.conversation = null;
    go(view);
  };
  const top = (view, iconName, title, current) =>
    h("button", { class: "mini-btn" + (current ? " current" : ""), title, "aria-label": title, "data-key": "mini-" + view, onclick: pick(view) }, icon(iconName));
  return h(
    "aside",
    { class: "sidebar mini", "data-app-sidebar": true, "aria-label": NAME },
    h("button", { class: "mini-btn mini-open", title: "Show the sidebar", "aria-label": "Show the sidebar", "data-key": "mini-open", onclick: () => setMini(false) }, icon("panel")),
    h(
      "div",
      { class: "mini-scroll" },
      top("sessions", "users", "All sessions", ui.view === "sessions"),
      top("messages", "message", "All conversations", ui.view === "messages" && !ui.filter),
      top("recent", "clock", "Recent messages", ui.view === "recent"),
      devices().map((d) => [
        h("div", { class: "mini-dev", title: (d.self ? "This device" : d.owner || d.id) + " · " + liveSessions(d).length }, deviceDot(d)),
        liveSessions(d).map((s) => {
          const key = s.addr || s.name;
          return h(
            "button",
            {
              class: "mini-face" + (ui.view === "messages" && ui.filter === key ? " current" : ""),
              title: s.name + (s.doing ? ": " + s.doing : ""),
              "aria-label": s.name,
              "data-key": "mini-s-" + s.name,
              onclick: () => {
                ui.filter = key;
                ui.conversation = null;
                go("messages");
              },
            },
            presence(s.name, d),
          );
        }),
      ]),
    ),
    h("button", { class: "mini-btn mini-foot" + (ui.view === "settings" ? " current" : ""), title: "Settings", "aria-label": "Settings", onclick: () => go("settings") }, icon("settings")),
  );
}

function sidebar() {
  if (mini) return miniSidebar();
  const devs = devices();
  return h(
    "aside",
    { class: "sidebar", "data-app-sidebar": true, "aria-label": NAME },
    h(
      "div",
      { class: "sidebar-header" },
      h("span", { class: "brand" }, NAME),
      h("button", { class: "fold-btn", title: "Hide the sidebar", "aria-label": "Hide the sidebar", "data-key": "fold", onclick: () => setMini(true) }, icon("panel")),
    ),
    h(
      "div",
      { class: "sidebar-content" },
      h(
        "div",
        { class: "sidebar-group g-top" },
        topRow("sessions", "users", "All sessions", allSessionsSub()),
        allButton(),
        topRow("recent", "clock", "Recent messages", plural(ui.messages.length, "message")),
      ),
      ui.notice && h("button", { class: "side-notice", title: "Dismiss", onclick: () => ((ui.notice = ""), render()) }, ui.notice),
      devs.map((d) =>
        h(
          "div",
          { class: "sidebar-group dev-group" },
          h(
            "div",
            {
              class: "device-link" + (d.reachable ? "" : " stale"),
              title: d.reachable ? d.id : d.id + ", last seen " + ago(when(d.lastSeen) || 0),
            },
            icon("laptop"),
            h("span", { class: "label" }, deviceDot(d), deviceLabel(d), h("span", { class: "dev-host" }, host(d.id))),
            d.self ? h("span", { class: "dev-you" }, "you") : !d.reachable && h("span", { class: "dev-you" }, "offline"),
            h("span", { class: "menu-count" }, liveSessions(d).length),
          ),
          liveSessions(d).length > 0 &&
            h(
              "ul",
              { class: "sidebar-menu side-sessions" },
              liveSessions(d).map((s) => {
                const key = s.addr || s.name;
                return h(
                  "li",
                  { class: "side-li" },
                  h(
                    "button",
                    {
                      class: "side-session",
                      "aria-current": ui.view === "messages" && ui.filter === key ? "page" : null,
                      "data-key": "ss-" + s.name,
                      title: s.doing || s.name,
                      onclick: () => {
                        ui.filter = key;
                        ui.conversation = null;
                        go("messages");
                      },
                    },
                    ...sideSessionBody(d, s),
                  ),
                  s.addr && hideButton(s),
                );
              }),
            ),
          quietFold(d),
          settledFold(d.id),
        ),
      ),
    ),
    h("div", { class: "sidebar-foot" }, h("ul", { class: "sidebar-menu" }, navButton("settings", "settings", null))),
  );
}

// Four states, as a colored dot on the face:
//   on    green   reachable, and active in the last five minutes
//   idle  yellow  reachable, but quiet for longer
//   dnd   red     running, but on do not disturb: messages wait in its inbox
//   off   grey    its device is unreachable
const STATE_WORDS = { on: "Active", idle: "Idle", dnd: "Do not disturb", off: "Offline" };
// What each state means, for a dot's tooltip.
const STATE_TIPS = {
  on: "Active in the last five minutes",
  idle: "Running and reachable, but quiet",
  dnd: "Do not disturb: messages wait in its inbox, with no turn and no notice, until it reads its inbox or the tag comes off",
  off: "Its device can't be reached",
};

// A model: tag (#211, derived for T3 sessions), shown beside the agent.
function modelOf(s) {
  const t = (s.tags || []).find((x) => x && typeof x.name === "string" && x.name.startsWith("model:"));
  return t ? t.name.slice(6) : "";
}

function sessionState(d, s) {
  if (!d || !d.reachable) return "off";
  if (s && (onDnd(s) || wakeNever(d, s))) return "dnd";
  const t = s && when(s.lastActive);
  return t && Date.now() - t < 5 * 60000 ? "on" : "idle";
}

// The do-not-disturb tag (#211) rides in every device's presence, so it works for any device's
// sessions: { tags: [{ name, src }] }, src "" (set by the session), "human" or "derived".
function onDnd(s) {
  return (s.tags || []).some((t) => t && t.name === "do-not-disturb");
}

// This device's settings say which of its sessions have wake-ups switched off.
function wakeNever(d, s) {
  if (!d.self || !ui.settings) return false;
  const own = ui.settings.sessionWake?.[s.name];
  return (own ? own.when : ui.settings.wake?.when) === "never";
}

// Quiet: running, but idle for a day, or hidden by a person with the hidden tag (#211). Agents
// in Claude Code, Codex and the rest never settle, so a session left open in a forgotten terminal
// stays in presence for good; this keeps it out of the way. An idle one comes back once it does
// anything. A hidden one stays hidden until someone shows it again: a person's tag stays until a
// person takes it off, and the fold still shows its dot.
const QUIET_MS = 24 * 60 * 60 * 1000;
const HIDDEN = "hidden";

// A Hide or Show this page has asked for, by address, until the state it polls agrees: another
// device's sessions carry the tag only once that device has read the change.
const hideWanted = new Map();

function isHidden(s) {
  const tagged = (s.tags || []).some((t) => t && t.name === HIDDEN);
  if (!hideWanted.has(s.addr)) return tagged;
  if (hideWanted.get(s.addr) === tagged) {
    hideWanted.delete(s.addr);
    return tagged;
  }
  return hideWanted.get(s.addr);
}

function isQuiet(d, s) {
  if (isHidden(s)) return true;
  const t = when(s.lastActive) || when(s.started);
  return !!t && Date.now() - t > QUIET_MS;
}

function liveSessions(d) {
  return d.sessions.filter((s) => !isQuiet(d, s));
}

function quietSessions(d) {
  return d.sessions.filter((s) => isQuiet(d, s));
}

// Hiding is a tag a person sets, so it holds on every device that shows the session.
async function setHidden(s, on) {
  ui.notice = "";
  try {
    await api("/api/tags", { method: "POST", body: JSON.stringify({ addr: s.addr, add: on ? [HIDDEN] : [], remove: on ? [] : [HIDDEN] }) });
    hideWanted.set(s.addr, on);
  } catch (e) {
    if (e instanceof AuthError) return deny(e.token);
    ui.notice = "Couldn't " + (on ? "hide " : "show ") + s.name + ": " + e.message;
  }
  render();
}

function hideButton(s) {
  const hidden = isHidden(s);
  return h(
    "button",
    {
      class: "side-hide",
      title: hidden ? "Show " + s.name + " again" : "Hide " + s.name + " until you show it again",
      "aria-label": hidden ? "Show " + s.name + " again" : "Hide " + s.name,
      "data-key": "hide-" + s.name,
      onclick: () => setHidden(s, !hidden),
    },
    icon(hidden ? "eye" : "eyeOff"),
  );
}

// Each device's quiet sessions, folded under its live ones.
function quietFold(d) {
  const rows = quietSessions(d);
  if (!rows.length) return null;
  ui.openQuiet = ui.openQuiet || new Set();
  const open = ui.openQuiet.has(d.id);
  return h(
    "div",
    { class: "settled" },
    h(
      "button",
      {
        class: "settled-toggle",
        "aria-expanded": open ? "true" : "false",
        "data-key": "quiet-" + d.id,
        onclick: () => {
          open ? ui.openQuiet.delete(d.id) : ui.openQuiet.add(d.id);
          render();
        },
      },
      icon(open ? "chevronDown" : "chevronRight", "settled-caret"),
      h("span", null, rows.length + " quiet"),
    ),
    open &&
      h(
        "div",
        { class: "settled-list" },
        rows.map((s) => {
          const key = s.addr || s.name;
          const t = when(s.lastActive);
          return h(
            "div",
            { class: "quiet-item" },
            h(
              "button",
              {
                class: "settled-row" + (ui.view === "messages" && ui.filter === key ? " current" : ""),
                title: s.doing || s.name,
                "data-key": "q-" + s.name,
                onclick: () => {
                  ui.filter = key;
                  ui.conversation = null;
                  go("messages");
                },
              },
              presence(s.name, d),
              h("span", { class: "settled-name truncate" }, s.name),
              h("span", { class: "settled-when" }, isHidden(s) ? "hidden" : t ? ago(t).replace(" ago", "") : ""),
            ),
            isHidden(s) && hideButton(s),
          );
        }),
      ),
  );
}

// A face with its presence dot inside it, so it still fits the two-face pair of a DM.
function dotted(name) {
  const a = avatar(name);
  const f = findSession(name);
  const st = f ? sessionState(f.device, f.session) : "off";
  a.classList.add("dotted");
  a.append(h("span", { class: "pres pres-" + st, title: name + ": " + STATE_TIPS[st] }));
  return a;
}

// A device: grey when unreachable, green when any of its sessions is active, else yellow.
function deviceState(d) {
  if (!d || !d.reachable) return "off";
  return d.sessions.some((s) => sessionState(d, s) === "on") ? "on" : "idle";
}
const DEVICE_WORDS = { on: "Online, a session is active", idle: "Online, all its sessions idle", off: "Unreachable" };

function deviceDot(d, cls = "") {
  const st = deviceState(d);
  return h("span", { class: ("dev-dot pres-" + st + " " + cls).trim(), title: DEVICE_WORDS[st] });
}

function presence(name, d) {
  const f = findSession(name);
  const st = sessionState(f ? f.device : d, f ? f.session : null);
  return h("span", { class: "pres-wrap" }, avatar(name), h("span", { class: "pres pres-" + st, title: STATE_TIPS[st] }));
}

// The sidebar's top rows: a picture, a title and a count under it.
function topRow(view, iconName, title, sub) {
  return h(
    "button",
    {
      class: "all-convs top-" + view,
      "aria-current": ui.view === view ? "page" : null,
      "data-key": "top-" + view,
      onclick: () => {
        ui.filter = null;
        go(view);
      },
    },
    h("span", { class: "all-ico" }, icon(iconName)),
    h("span", { class: "all-text" }, h("span", { class: "all-title" }, title), h("span", { class: "all-sub" }, sub)),
  );
}

// Settled sessions: stopped, or settled in T3, so their device leaves them out of presence (#189).
// The page knows them only from messages, by address, so each is grouped under the device its
// address names. Hidden by default, never gone.
function settledFold(id) {
  const rows = settledOn(id);
  if (!rows.length) return null;
  ui.openSettled = ui.openSettled || new Set();
  const open = ui.openSettled.has(id);
  return h(
    "div",
    { class: "settled" },
    h(
      "button",
      {
        class: "settled-toggle",
        "aria-expanded": open ? "true" : "false",
        "data-key": "fold-" + id,
        onclick: () => {
          open ? ui.openSettled.delete(id) : ui.openSettled.add(id);
          render();
        },
      },
      icon(open ? "chevronDown" : "chevronRight", "settled-caret"),
      h("span", null, plural(rows.length, "settled session")),
    ),
    open && h("div", { class: "settled-list" }, rows.map(settledRow)),
  );
}

// Worked out again only when the state or the messages change: a render asks many times.
let settledMemo = { state: null, n: -1, cursor: null, learned: -1, rows: [], names: new Set() };

function settledSessions() {
  const memo = settledMemo;
  if (memo.state === ui.state && memo.n === ui.messages.length && memo.cursor === ui.cursor && memo.learned === namesLearned) return memo.rows;
  const rows = findSettled();
  settledMemo = { state: ui.state, n: ui.messages.length, cursor: ui.cursor, learned: namesLearned, rows, names: new Set(rows.map((r) => r.name)) };
  return rows;
}

function findSettled() {
  // Running by name or by address: presence can still carry a session's device/session while the
  // messages already have the name that landed for it.
  const running = new Set();
  const runningAddrs = new Set();
  for (const d of ui.state?.devices || []) {
    for (const s of d.sessions || []) {
      running.add(s.name);
      running.add(nameOf(s.addr, s.name));
      if (s.addr) runningAddrs.add(s.addr);
    }
  }
  const seen = new Map();
  const note = (name, addr, m) => {
    if (!name || running.has(name) || (addr && runningAddrs.has(addr)) || name.includes("/")) return;
    const r = seen.get(name) || { name, device: "", last: 0, lastMsg: null, peers: new Set() };
    if (addr && addr.includes("/")) {
      r.device = addr.split("/")[0];
      r.addr = addr;
    }
    const t = when(m.at) || 0;
    if (t > r.last) {
      r.last = t;
      r.lastMsg = m;
    }
    seen.set(name, r);
  };
  for (const m of ui.messages) {
    note(fromName(m), m.fromAddr, m);
    if (m.toKind === "session") note(toName(m), m.toAddr, m);
    if (m.toKind === "session") {
      const a = seen.get(fromName(m));
      if (a) a.peers.add(toName(m));
      const b = seen.get(toName(m));
      if (b) b.peers.add(fromName(m));
    }
  }
  return [...seen.values()].sort((a, b) => b.last - a.last);
}

function settledOn(id) {
  return settledSessions().filter((r) => r.device === id);
}

function isSettled(name) {
  settledSessions();
  return settledMemo.names.has(name) && !findSession(name);
}

function settledRow(r) {
  return h(
    "button",
    {
      class: "settled-row" + (ui.view === "messages" && ui.filter && nameOf(ui.filter, ui.filter) === r.name ? " current" : ""),
      title: r.name + ", last seen " + ago(r.last),
      "data-key": "settled-" + r.name,
      onclick: () => {
        ui.filter = r.addr || r.name;
        ui.conversation = null;
        go("messages");
      },
    },
    avatar(r.name),
    h("span", { class: "settled-name truncate" }, r.name),
    h("span", { class: "settled-when" }, ago(r.last).replace(" ago", "")),
  );
}

// All sessions' count: those running, and those settled if there are any, else the devices.
function allSessionsSub() {
  const settled = settledSessions().length;
  const live = devices().reduce((n, d) => n + liveSessions(d).length, 0);
  return plural(live, "session") + " · " + (settled ? settled + " settled" : plural(devices().length, "device"));
}

// Who a device belongs to, for its heading; its host name goes beside it.
function deviceLabel(d) {
  return d.self ? "This device" : d.owner || d.id;
}

// A device ID ends in four hex digits that tell two clones on one machine apart.
function host(id) {
  return String(id).replace(/-[0-9a-f]{4}$/, "");
}

// Every conversation, between the sidebar's other two top rows.
function allButton() {
  const all = ui.view === "messages" && !ui.filter;
  const count = allConversations().length;
  return h(
    "button",
    {
      class: "all-convs",
      "aria-current": all ? "page" : null,
      "data-key": "all",
      onclick: () => {
        ui.filter = null;
        ui.conversation = null;
        go("messages");
      },
    },
    h("span", { class: "all-ico" }, icon("message")),
    h("span", { class: "all-text" }, h("span", { class: "all-title" }, "All conversations"), h("span", { class: "all-sub" }, plural(count, "conversation"))),
  );
}

function allConversations() {
  const keep = ui.filter;
  ui.filter = null;
  const list = conversations();
  ui.filter = keep;
  return list;
}

// A session in the sidebar: its face and state, what it's doing, and its branch and pull request.
function sideSessionBody(d, s) {
  const prs = ui.state?.prs || {};
  const pr = s.branch && Object.hasOwn(prs, s.branch) ? prs[s.branch] : null;
  return [
    presence(s.name, d),
    h(
      "span",
      { class: "side-session-text" },
      h("span", { class: "side-session-name truncate" }, s.name),
      h("span", { class: "side-session-doing truncate" }, s.doing || s.agent || ""),
      (s.branch || pr) &&
        h(
          "span",
          { class: "g-meta" },
          s.branch && h("span", { class: "g-branch truncate" }, icon("branch"), h("span", { class: "truncate" }, s.branch)),
          pr && h("span", { class: "g-pr" + (pr.draft ? " draft" : "") }, "#" + pr.number),
        ),
    ),
  ];
}

function stateWord(d, s) {
  const st = sessionState(d, s);
  const t = when(s.lastActive);
  if (st === "on") return "Active now";
  if (st === "idle") return t ? "Idle " + ago(t).replace(" ago", "") : "Idle";
  return STATE_WORDS[st];
}

// Every session, as cards under its device; tabs show the quiet and the settled ones.
function sessionsPage() {
  if (!ui.state) return h("div", { class: "page" });
  const devs = devices();
  const total = devs.reduce((n, d) => n + liveSessions(d).length, 0);
  const quiet = devs.reduce((n, d) => n + quietSessions(d).length, 0);
  const tab = ui.sessTab || "running";
  return h(
    "div",
    { class: "g-page" },
    h(
      "header",
      { class: "g-page-head g-page-head-row" },
      h("div", null, h("h1", null, "All sessions"), h("p", null, plural(total, "session") + " running on " + plural(devs.length, "device"))),
      h(
        "div",
        { class: "g-tabs", role: "tablist" },
        [
          ["running", "Running", total],
          ["quiet", "Quiet", quiet],
          ["settled", "Settled", settledSessions().length],
        ].map(([id, label, n]) =>
          h(
            "button",
            {
              role: "tab",
              "aria-selected": tab === id ? "true" : "false",
              "data-key": "tab-" + id,
              onclick: () => {
                ui.sessTab = id;
                render();
              },
            },
            label,
            h("span", { class: "g-tab-n" }, n),
          ),
        ),
      ),
    ),
    tab === "settled" ? settledPage(devs) : tab === "quiet" ? quietPage(devs) : devs.map((d) =>
      h(
        "section",
        { class: "g-dev" },
        h(
          "h2",
          { class: "g-dev-head" },
          deviceDot(d),
          h("span", null, d.self ? "This device" : d.owner || d.id),
          h("span", { class: "g-dev-host" }, host(d.id)),
          h("span", { class: "g-dev-n" }, d.reachable ? plural(liveSessions(d).length, "session") : "Offline · seen " + ago(when(d.lastSeen) || 0)),
        ),
        liveSessions(d).length === 0
          ? h("p", { class: "g-none" }, "No sessions")
          : h("div", { class: "g-cards" }, liveSessions(d).map((s) => sessionCard(d, s))),
      ),
    ),
  );
}

function quietPage(devs) {
  const withQuiet = devs.filter((d) => quietSessions(d).length > 0);
  if (!withQuiet.length) return h("p", { class: "g-none" }, "No quiet sessions.");
  return withQuiet.map((d) =>
    h(
      "section",
      { class: "g-dev" },
      h(
        "h2",
        { class: "g-dev-head" },
        deviceDot(d),
        h("span", null, deviceLabel(d)),
        h("span", { class: "g-dev-host" }, host(d.id)),
        h("span", { class: "g-dev-n" }, quietSessions(d).length + " quiet"),
      ),
      h("div", { class: "g-cards" }, quietSessions(d).map((s) => sessionCard(d, s))),
    ),
  );
}

function settledPage(devs) {
  const q = (ui.sq || "").toLowerCase();
  const all = settledSessions().filter((r) => !q || r.name.includes(q) || (r.lastMsg?.subject || "").toLowerCase().includes(q));
  const byDev = new Map();
  for (const r of all) {
    if (!byDev.has(r.device)) byDev.set(r.device, []);
    byDev.get(r.device).push(r);
  }
  const label = (id) => {
    const d = devs.find((x) => x.id === id);
    return d ? (d.self ? "This device" : d.owner || id) : id || "Unknown device";
  };
  return [
    h(
      "label",
      { class: "g-search g-search-wide" },
      icon("search"),
      h("input", {
        type: "search",
        placeholder: "Search settled sessions",
        "data-key": "sq",
        value: ui.sq || null,
        oninput: (e) => {
          ui.sq = e.target.value;
          render();
        },
      }),
    ),
    [...byDev].map(([id, rows]) =>
      h(
        "section",
        { class: "g-dev" },
        h("h2", { class: "g-dev-head" }, h("span", null, label(id)), h("span", { class: "g-dev-host" }, host(id)), h("span", { class: "g-dev-n" }, plural(rows.length, "settled session"))),
        h(
          "div",
          { class: "g-settled-list" },
          rows.map((r) =>
            h(
              "button",
              {
                class: "g-settled",
                onclick: () => {
                  ui.filter = r.addr || r.name;
                  ui.conversation = null;
                  go("messages");
                },
              },
              avatar(r.name),
              h("span", { class: "g-settled-name truncate" }, r.name),
              h("span", { class: "g-settled-last truncate" }, r.lastMsg ? r.lastMsg.subject : ""),
              h("span", { class: "g-settled-chats" }, plural(r.peers.size, "chat")),
              h("span", { class: "g-settled-when" }, ago(r.last)),
            ),
          ),
        ),
      ),
    ),
    all.length === 0 && h("p", { class: "g-none" }, q ? "Nothing matches." : "No settled sessions."),
  ];
}

function sessionCard(d, s) {
  const prs = ui.state.prs || {};
  const pr = s.branch && Object.hasOwn(prs, s.branch) ? prs[s.branch] : null;
  const keep = ui.filter;
  ui.filter = s.addr || s.name;
  const convs = conversations().filter((c) => c.kind === "dm").length;
  ui.filter = keep;
  const st = stateWord(d, s);
  return h(
    "button",
    {
      class: "g-card" + (d.reachable ? "" : " stale"),
      "data-key": "card-" + s.name,
      onclick: () => {
        ui.filter = s.addr || s.name;
        ui.conversation = null;
        go("messages");
      },
    },
    h(
      "span",
      { class: "g-card-top" },
      presence(s.name, d),
      h("span", { class: "g-card-id" }, h("span", { class: "g-card-name truncate" }, s.name), h("span", { class: "g-card-agent truncate" }, [s.agent, modelOf(s)].filter(Boolean).join(" · "))),
      st && h("span", { class: "g-state g-state-" + sessionState(d, s) }, st),
    ),
    h("span", { class: "g-card-doing" + (s.doing ? "" : " none") }, s.doing || "No status set"),
    h(
      "span",
      { class: "g-card-foot" },
      s.branch && h("span", { class: "g-branch truncate" }, icon("branch"), h("span", { class: "truncate" }, s.branch)),
      pr && h("span", { class: "g-pr" + (pr.draft ? " draft" : "") }, "#" + pr.number),
      h("span", { class: "g-card-chats" }, plural(convs, "chat")),
    ),
  );
}

// Recent: the newest messages first, as many as a render can build quickly. One opens its
// conversation.
const RECENT_MAX = 300;
function recentPage() {
  const list = [...ui.messages].sort((a, b) => b.sortAt - a.sortAt).slice(0, RECENT_MAX);
  const out = [];
  let day = null;
  for (const m of list) {
    const at = when(m.at);
    const d = at && new Date(at).toDateString();
    if (d && d !== day) {
      out.push(h("div", { class: "chat-day" }, h("span", null, dayLabel(at))));
      day = d;
    }
    out.push(recentItem(m, at));
  }
  return h(
    "div",
    { class: "g-page g-recent" },
    h(
      "header",
      { class: "g-page-head" },
      h("h1", null, "Recent messages"),
      h("p", null, ui.messages.length > RECENT_MAX ? "Newest " + RECENT_MAX + " of " + ui.messages.length : plural(ui.messages.length, "message")),
    ),
    list.length ? h("div", { class: "g-feed" }, out) : h("p", { class: "g-none" }, "No messages yet"),
  );
}

function recentItem(m, at) {
  const from = fromName(m);
  const c = conversationOf(m);
  const to = m.toKind === "all" ? [icon("broadcast"), "Everyone"] : m.toKind === "device" ? [icon("laptop"), m.to] : [avatar(toName(m)), toName(m)];
  const open = () => {
    ui.filter = null;
    ui.conversation = c.key;
    ui.conversationAnchor = m.id;
    go("messages");
  };
  return h(
    "article",
    { class: "chat-msg chat-msg-first g-item", "data-id": m.id, tabindex: 0, role: "button", "aria-label": "Open the conversation", onclick: open, onkeydown: (e) => (e.key === "Enter" ? open() : null) },
    avatar(from),
    h(
      "div",
      { class: "chat-msg-main" },
      h(
        "div",
        { class: "chat-msg-head" },
        h("span", { class: "chat-msg-from truncate" }, from),
        h("span", { class: "g-to" }, icon("arrowRight", "g-to-arrow"), h("span", { class: "g-to-who" }, to)),
        h("span", { class: "chat-msg-time", title: messageDetail(m) }, at && new Date(at).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })),
      ),
      m.subject && h("div", { class: "chat-msg-subject" }, m.subject),
      m.body && h("div", { class: "chat-msg-body rich" }, rich(m.body)),
    ),
  );
}

// Everyone reaches every session in the repo; a device's chat reaches every session on it. Anyone
// who has posted is in it too, running or not.
function groupMembers(c) {
  const posted = new Map();
  const addrs = new Map(); // where each sender posted from, for one that isn't running
  for (const m of c.messages) {
    posted.set(fromName(m), (posted.get(fromName(m)) || 0) + 1);
    if (m.fromAddr) addrs.set(fromName(m), m.fromAddr);
  }
  const rows = [];
  for (const d of devices()) {
    if (c.kind === "device" && d.id !== c.title) continue;
    for (const s of d.sessions) rows.push({ name: s.name, d, s, posted: posted.get(s.name) || 0 });
  }
  for (const [name, n] of posted) if (!rows.some((r) => r.name === name)) rows.push({ name, addr: addrs.get(name), d: null, s: null, posted: n });
  return rows;
}

function membersButton(c) {
  const rows = groupMembers(c);
  const pile = rows.slice(0, 5);
  return h(
    "div",
    { class: "g-members-wrap" },
    h(
      "button",
      {
        class: "g-members",
        "aria-expanded": ui.membersOpen ? "true" : "false",
        "data-key": "members",
        onclick: (e) => {
          e.stopPropagation();
          ui.membersOpen = !ui.membersOpen;
          render();
        },
      },
      h("span", { class: "g-pile" }, pile.map((r) => avatar(r.name))),
      h("span", null, plural(rows.length, "member")),
      icon("chevronDown", "g-members-chev"),
    ),
    ui.membersOpen && membersPanel(c, rows),
  );
}

function membersPanel(c, rows) {
  const groups = new Map();
  for (const r of rows) {
    const key = r.d ? (r.d.self ? "This device" : r.d.owner || r.d.id) : "Not running";
    if (!groups.has(key)) groups.set(key, { d: r.d, host: r.d ? host(r.d.id) : "", rows: [] });
    groups.get(key).rows.push(r);
  }
  return h(
    "div",
    { class: "g-members-panel", role: "dialog", "aria-label": "Members", onclick: (e) => e.stopPropagation() },
    h("div", { class: "g-mp-head" }, c.kind === "all" ? "Everyone in this repo" : "Every session on " + host(c.title)),
    [...groups].map(([title, g]) => [
      h("div", { class: "g-mp-sec" }, g.d && deviceDot(g.d), title, g.host && h("span", null, g.host), h("span", { class: "g-mp-n" }, g.rows.length)),
      g.rows.map((r) =>
        h(
          "button",
          {
            class: "g-mp-row" + (r.d && !r.d.reachable ? " stale" : ""),
            onclick: () => {
              ui.membersOpen = false;
              const f = findSession(r.name);
              ui.filter = f ? f.session.addr || r.name : r.addr || r.name;
              ui.conversation = null;
              go("messages");
            },
          },
          r.d ? presence(r.name, r.d) : avatar(r.name),
          h("span", { class: "g-mp-text" }, h("span", { class: "g-mp-name truncate" }, r.name), h("span", { class: "g-mp-doing truncate" }, r.s ? r.s.doing || r.s.agent || "" : "Not running")),
          r.posted > 0 && h("span", { class: "g-mp-posted" }, r.posted === 1 ? "1 post" : r.posted + " posts"),
        ),
      ),
    ]),
  );
}
document.addEventListener("click", () => {
  if (ui.membersOpen) {
    ui.membersOpen = false;
    render();
  }
});

function topbar() {
  let meta = "";
  if (ui.conn === "down") {
    meta = h("span", { class: "device-state" }, h("span", { class: "dot warn" }), "Disconnected");
  }
  return h(
    "header",
    { class: "topbar" },
    h("span", { class: "topbar-brand brand" }, NAME),
    h("h1", null, TITLES[ui.view]),
    h(
      "div",
      { class: "mobile-nav segmented", role: "group", "aria-label": "Views" },
      VIEWS.map((v) =>
        h(
          "button",
          {
            "aria-pressed": ui.view === v ? "true" : "false",
            "data-key": "m-" + v,
            onclick: () => {
              if (v === "messages") {
                ui.filter = null;
                ui.conversation = null;
              }
              go(v);
            },
          },
          TITLES[v],
        ),
      ),
    ),
    meta && h("div", { class: "topbar-meta" }, meta),
  );
}

function page() {
  if (ui.view === "sessions") return sessionsPage();
  if (ui.view === "recent") return recentPage();
  if (ui.view === "settings") return settingsPage();
  return conversationsPage();
}

// Tags (#198), under an open session's name. Each chip says where the tag came from; a human can
// take one off or add one. The change goes to the session's own device, which applies it over what
// the session set, so on another device it shows once that device has read it.
const TAG_SOURCES = { human: "Set by a human", derived: "Worked out by its daemon" };

function tagsLine(s) {
  const tags = Array.isArray(s.tags) ? s.tags : [];
  const addr = s.addr || s.name;
  const key = "t-" + addr;
  const busy = ui.tagging.has(key);
  // The row opens the session's messages; nothing in here should.
  const stop = (e) => e.stopPropagation();
  return h(
    "div",
    { class: "session-tags", onclick: stop, onkeydown: stop },
    tags.map((t) =>
      h(
        "span",
        {
          class: "badge tag badge-outline",
          title: TAG_SOURCES[t.src] || "Set by the session",
        },
        t.name,
        h(
          "button",
          {
            class: "tag-remove",
            "aria-label": "Remove " + t.name,
            title: "Remove " + t.name,
            "data-key": key + "-rm-" + t.name,
            disabled: busy,
            onclick: () => changeTags(addr, key, [], [t.name]),
          },
          "×",
        ),
      ),
    ),
    ui.tagInput === key
      ? tagField(addr, key)
      : h(
          "button",
          {
            class: "btn btn-ghost btn-xs",
            "data-key": key + "-add",
            disabled: busy,
            onclick: () => {
              ui.tagInput = key;
              delete ui.rowErrors[key];
              render();
              root.querySelector(`[data-key="${CSS.escape(key + "-field")}"]`)?.focus();
            },
          },
          "+ Tag",
        ),
    ui.rowErrors[key] && h("span", { class: "row-error" }, ui.rowErrors[key]),
  );
}

function tagField(addr, key) {
  const close = () => {
    ui.tagInput = null;
    render();
  };
  const input = h("input", {
    type: "text",
    placeholder: "reviewing, model:opus-5.5",
    "aria-label": "Tags to add",
    "data-key": key + "-field",
    autocomplete: "off",
    spellcheck: "false",
    onkeydown: (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        close();
      } else if (e.key === "Enter") {
        e.preventDefault();
        const add = e.target.value.split(/[\s,]+/).filter(Boolean);
        ui.tagInput = null;
        if (add.length > 0) changeTags(addr, key, add, []);
        else render();
      }
    },
    onblur: (e) => {
      if (ui.tagInput === key && !e.target.value.trim()) close();
    },
  });
  return h("span", { class: "field" }, input);
}

async function changeTags(addr, key, add, remove) {
  ui.tagging.add(key);
  delete ui.rowErrors[key];
  render();
  try {
    await api("/api/tags", { method: "POST", body: JSON.stringify({ addr, add, remove }) });
  } catch (e) {
    if (e instanceof AuthError) return deny(e.token);
    ui.rowErrors[key] = e.message;
  }
  ui.tagging.delete(key);
  render();
  poll();
}

// Messages.

// A session's messages: those it sent, and those it was sent, to it, its device or everyone. The
// filter is the session's address, which stays put when its name lands after it was chosen. Once
// it has a name, any address now called by that name is the same conversation: one that came back
// under a new session ID keeps its name.
function visibleMessages() {
  const f = ui.filter;
  if (!f) return ui.messages;
  const name = nameOf(f, f);
  const slash = f.indexOf("/");
  const device = slash > 0 ? f.slice(0, slash) : findSession(name)?.device.id;
  const is = (addr, shown) => (addr ? addr === f : shown === name) || (name !== f && nameOf(addr, shown) === name);
  return ui.messages.filter(
    (m) =>
      is(m.fromAddr, m.from) ||
      (m.toKind === "session" && is(m.toAddr, m.to)) ||
      (m.toKind === "device" && m.to === device) ||
      m.toKind === "all",
  );
}

function noMessages() {
  return ui.filter ? "No messages with " + nameOf(ui.filter, ui.filter) : "No messages";
}

// Who sent a message and when, as far as the page knows.
function messageDetail(m) {
  const at = when(m.at);
  const sender = findSession(fromName(m));
  return [sender && [sender.session.agent, sender.device.id].filter(Boolean).join(" on "), at && stamp(at)]
    .filter(Boolean)
    .join(" · ");
}

// Conversations: two sessions' messages to each other, a device's, and everyone's. There is no
// thread on the bus, so this is all a conversation can be. They are keyed by name, not address, so
// a session that came back under a new session ID stays in the same one.
function conversationOf(m) {
  if (m.toKind === "all") return { key: "all", kind: "all", title: "Everyone" };
  if (m.toKind === "device") return { key: "device:" + m.to, kind: "device", title: m.to };
  const pair = [...new Set([fromName(m), toName(m)])].sort();
  return { key: "dm:" + JSON.stringify(pair), kind: "dm", title: pair.join(" & "), pair };
}

// Newest conversation first, and each one's messages oldest first. The page holds messages as they
// arrived, device by device, so a reply can be held ahead of what it answers; sortAt, from the
// senders' clocks, puts them back in order.
function conversations() {
  const byKey = new Map();
  for (const m of visibleMessages()) {
    const c = conversationOf(m);
    if (!byKey.has(c.key)) byKey.set(c.key, { ...c, messages: [] });
    byKey.get(c.key).messages.push(m);
  }
  const list = [...byKey.values()];
  for (const c of list) c.messages.sort((a, b) => a.sortAt - b.sortAt);
  const latest = (c) => c.messages[c.messages.length - 1].sortAt;
  return list.sort((a, b) => latest(b) - latest(a));
}

// The same name always gets the same color. The classes are in style.css: the page's content
// policy doesn't allow an inline style.
function avatar(name) {
  let n = 0;
  for (const ch of name) n = (n * 31 + ch.codePointAt(0)) % 997;
  return h("span", { class: "avatar avatar-" + (n % 5), "aria-hidden": "true" }, (name[0] || "?").toUpperCase());
}

// A group chat is named for whose it is, and its face is its members'.
function groupName(c) {
  if (c.kind === "all") return "Everyone";
  const d = (ui.state?.devices || []).find((x) => x.id === c.title);
  if (!d) return host(c.title);
  if (d.self) return "This device";
  const o = d.owner || "";
  return o ? o[0].toUpperCase() + o.slice(1) + "'s " + (devices().filter((x) => x.owner === d.owner).length > 1 ? host(d.id) : "device") : d.id;
}

function groupHost(c) {
  return c.kind === "device" ? host(c.title) : "";
}

function groupSummary(c) {
  const rows = groupMembers(c);
  const on = rows.filter((r) => r.d && sessionState(r.d, r.s) === "on").length;
  return { rows, on };
}

// Up to four members' faces in a square, the last showing how many more there are.
function groupIcon(c) {
  const { rows } = groupSummary(c);
  const shown = rows.length > 4 ? rows.slice(0, 3) : rows.slice(0, 4);
  return h(
    "span",
    { class: "g-grid g-grid-" + Math.min(rows.length, 4), "aria-hidden": "true" },
    shown.map((r) => avatar(r.name)),
    rows.length > 4 && h("span", { class: "avatar g-more" }, "+" + (rows.length - 3)),
  );
}

function conversationIcon(c) {
  if (c.kind !== "dm") return groupIcon(c);
  if (c.pair.length === 1) return dotted(c.pair[0]);
  return h("span", { class: "avatar-pair" }, c.pair.map(dotted));
}

let chatMoved = false; // the open conversation has just changed key, and is still the one being read

function conversationsPage() {
  const list = conversations();
  // On a wide page a conversation is always open, and stays open while others get newer. On a
  // narrow one the list and a conversation take turns, so the list is where it starts.
  // A name landing changes a conversation's key, so one that has gone is looked for by a message
  // it held before settling for the newest.
  if (ui.conversation && !list.some((c) => c.key === ui.conversation)) {
    ui.conversation = list.find((c) => c.messages.some((m) => m.id === ui.conversationAnchor))?.key || null;
    chatMoved = ui.conversation != null;
  }
  if (!ui.conversation && list.length && !NARROW.matches) ui.conversation = list[0].key;
  const open = list.find((c) => c.key === ui.conversation);
  ui.conversationAnchor = open?.messages[open.messages.length - 1].id;
  return h(
    "div",
    { class: "page chat-page" },
    // A picked session keeps its header, and so its tags, before it has any messages.
    list.length === 0 && !ui.filter
      ? h("div", { class: "blank" }, noMessages())
      : h(
          "div",
          { class: "chat" + (open ? " chat-open" : "") },
          conversationList(list),
          open && conversationPane(open),
        ),
  );
}

// With a session picked, a chat with it is called by the other session's name.
function otherIn(c) {
  if (!ui.filter || c.kind !== "dm") return null;
  const me = nameOf(ui.filter, ui.filter);
  return c.pair.includes(me) ? c.pair.find((n) => n !== me) || me : null;
}

// The middle column: whose conversations they are, a search, then direct messages and group chats
// in one list. Direct messages with a settled session fold away at the end.
function conversationList(list) {
  const name = ui.filter && nameOf(ui.filter, ui.filter);
  const f = name && findSession(name);
  const q = (ui.cq || "").toLowerCase();
  const hit = (c) => !q || (otherIn(c) || c.title).toLowerCase().includes(q) || c.messages.some((m) => (m.subject + " " + (m.body || "")).toLowerCase().includes(q));
  // One list, newest first, so a busy group chat is never below every quiet DM. The switch
  // narrows it to direct messages or group chats.
  const kind = ui.convKind || "all";
  const wanted = (c) => kind === "all" || (kind === "dm") === (c.kind === "dm");
  const old = (c) => c.kind === "dm" && c.pair.some((n) => isSettled(n));
  const rows = list.filter((c) => wanted(c) && hit(c) && (q || name || !old(c)));
  const older = q || name || kind === "group" ? [] : list.filter((c) => old(c));
  const count = (k) => list.filter((c) => (k === "all" || (k === "dm") === (c.kind === "dm")) && hit(c) && (q || name || !old(c))).length;
  return h(
    "nav",
    { class: "chat-list g-list", "aria-label": "Conversations", onscroll: (e) => (ui.chatListTop = e.target.scrollTop) },
    h(
      "div",
      { class: "g-head" },
      name
        ? [
            h("div", { class: "g-head-who" }, f ? presence(name, f.device) : avatar(name), h("h2", { class: "truncate" }, name)),
            f && f.session.doing && h("p", { class: "g-head-doing" }, f.session.doing),
            f && tagsLine(f.session),
            !f && h("p", { class: "g-head-doing" }, "Settled · " + ago(settledSessions().find((r) => r.name === name)?.last || 0)),
          ]
        : [h("h2", null, "All conversations"), h("p", { class: "g-head-doing" }, plural(list.length, "conversation"))],
    ),
    h(
      "label",
      { class: "g-search" },
      icon("search"),
      h("input", {
        type: "search",
        placeholder: "Search conversations",
        "data-key": "cq",
        value: ui.cq || null,
        oninput: (e) => {
          ui.cq = e.target.value;
          render();
        },
      }),
    ),
    h(
      "div",
      { class: "g-tabs g-kinds", role: "tablist", "aria-label": "Which conversations" },
      [
        ["all", "All"],
        ["dm", "Direct"],
        ["group", "Group"],
      ].map(([id, label]) =>
        h(
          "button",
          {
            role: "tab",
            "aria-selected": kind === id ? "true" : "false",
            "data-key": "kind-" + id,
            onclick: () => {
              ui.convKind = id;
              render();
            },
          },
          label,
          h("span", { class: "g-tab-n" }, count(id)),
        ),
      ),
    ),
    rows.map(conversationRow),
    older.length > 0 &&
      h(
        "button",
        {
          class: "g-sec g-sec-toggle",
          "aria-expanded": ui.showOlder ? "true" : "false",
          "data-key": "older",
          onclick: () => {
            ui.showOlder = !ui.showOlder;
            render();
          },
        },
        h("span", null, icon(ui.showOlder ? "chevronDown" : "chevronRight", "settled-caret"), "With settled sessions"),
        h("span", { class: "g-sec-n" }, older.length),
      ),
    ui.showOlder && older.map(conversationRow),
    !rows.length && (q || !older.length) && h("p", { class: "g-none" }, q ? "Nothing matches." : kind === "dm" ? "No direct messages" : kind === "group" ? "No group chats" : "No conversations"),
  );
}

function conversationRow(c) {
  const last = c.messages[c.messages.length - 1];
  const at = when(last.at);
  const o = otherIn(c);
  const me = ui.filter && nameOf(ui.filter, ui.filter);
  return h(
    "button",
    {
      class: "chat-row",
      "aria-current": c.key === ui.conversation ? "true" : null,
      "data-key": "c-" + c.key,
      onclick: () => {
        ui.conversation = c.key;
        ui.conversationAnchor = c.messages[c.messages.length - 1].id; // the key can be out of date
        render();
      },
    },
    o ? dotted(o) : conversationIcon(c),
    h(
      "span",
      { class: "chat-row-text" },
      h(
        "span",
        { class: "chat-row-line" },
        c.kind !== "dm"
          ? [
              h("span", { class: "chat-row-title truncate", title: c.title }, groupName(c)),
              h("span", { class: "g-row-count" }, groupSummary(c).rows.length),
            ]
          : h("span", { class: "chat-row-title truncate", title: c.title }, o || c.title),
        at && agoEl("span", "chat-row-time", at),
      ),
      h("span", { class: "chat-row-preview truncate" }, (o && fromName(last) === o ? "" : o && fromName(last) === me ? "You: " : fromName(last) + ": ") + last.subject),
    ),
  );
}

function conversationPane(c) {
  const senders = new Set(c.messages.map(fromName));
  const o = otherIn(c);
  const of = o && findSession(o);
  const gs = c.kind !== "dm" ? groupSummary(c) : null;
  const about = gs
    ? [groupHost(c), plural(gs.rows.length, "member"), gs.on + " active"].filter(Boolean).join(" · ")
    : o
      ? of
        ? of.session.doing || of.session.agent || ""
        : "Not running"
      : "";
  return h(
    "section",
    { class: "chat-pane", "aria-label": o || c.title },
    h(
      "header",
      { class: "chat-head" },
      h(
        "button",
        {
          class: "chat-back",
          "aria-label": "All conversations",
          "data-key": "chat-back",
          onclick: () => {
            ui.conversation = null;
            render();
          },
        },
        icon("arrowLeft"),
      ),
      o ? dotted(o) : conversationIcon(c),
      h(
        "div",
        { class: "chat-head-text" },
        h("h2", { class: "chat-title truncate", title: c.title }, gs ? groupName(c) : o || c.title),
        about && h("div", { class: "chat-about truncate", title: [...senders].join(", ") }, about),
      ),
      c.kind !== "dm" && membersButton(c),
    ),
    h("div", { class: "chat-thread", "data-conversation": c.key }, chatMessages(c)),
  );
}

// A sender's messages run together under one header, as long as they come close together.
const RUN_MS = 7 * 60 * 1000;

function chatMessages(c) {
  const out = [];
  let prev = null;
  let prevDay = null;
  for (const m of c.messages) {
    const at = when(m.at);
    const day = at && new Date(at).toDateString();
    if (day && day !== prevDay) {
      out.push(h("div", { class: "chat-day" }, h("span", null, dayLabel(at))));
      prev = null;
      prevDay = day;
    }
    const from = fromName(m);
    const head = !prev || fromName(prev) !== from || !at || at - (when(prev.at) || 0) > RUN_MS;
    out.push(chatMessage(m, from, at, head));
    prev = m;
  }
  return out;
}

function dayLabel(t) {
  const today = new Date();
  if (new Date(t).toDateString() === today.toDateString()) return "Today";
  today.setDate(today.getDate() - 1);
  if (new Date(t).toDateString() === today.toDateString()) return "Yesterday";
  return new Date(t).toLocaleDateString(undefined, { year: "numeric", month: "long", day: "numeric" });
}

function chatMessage(m, from, at, head) {
  const sender = findSession(from);
  return h(
    "article",
    { class: "chat-msg" + (head ? " chat-msg-first" : ""), "data-id": m.id, title: head ? null : messageDetail(m) },
    head ? avatar(from) : h("span", { class: "avatar-gap" }),
    h(
      "div",
      { class: "chat-msg-main" },
      head &&
        h(
          "div",
          { class: "chat-msg-head" },
          h("span", { class: "chat-msg-from truncate" }, from),
          sender && h("span", { class: "chat-msg-via truncate" }, [sender.session.agent, sender.device.self ? null : sender.device.id].filter(Boolean).join(" · ")),
          h("span", { class: "chat-msg-time", title: messageDetail(m) }, at && new Date(at).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })),
        ),
      m.subject && h("div", { class: "chat-msg-subject" }, m.subject),
      m.body && h("div", { class: "chat-msg-body rich" }, rich(m.body)),
    ),
    h(
      "div",
      { class: "msg-tools" },
      h(
        "button",
        {
          title: "Copy",
          "aria-label": "Copy message",
          onclick: () => navigator.clipboard?.writeText((m.subject ? m.subject + "\n\n" : "") + (m.body || "")),
        },
        icon("copy"),
      ),
    ),
  );
}

// Message bodies are Markdown-ish text from agents. A small subset becomes structure: paragraphs,
// lists, fenced code, inline code, bold and https links. All of it reaches the page as text nodes.
function rich(text) {
  const out = [];
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (/^\s*```/.test(line)) {
      const code = [];
      i++;
      while (i < lines.length && !/^\s*```/.test(lines[i])) code.push(lines[i++]);
      i++;
      out.push(h("pre", null, h("code", null, code.join("\n"))));
      continue;
    }
    const bullet = /^\s*[-*•] +/;
    const num = /^\s*\d+[.)] +/;
    if (bullet.test(line) || num.test(line)) {
      const re = bullet.test(line) ? bullet : num;
      const items = [];
      while (i < lines.length && lines[i].trim()) {
        if (re.test(lines[i])) items.push(lines[i].replace(re, ""));
        else if (items.length) items[items.length - 1] += " " + lines[i].trim();
        else break;
        i++;
      }
      out.push(h(re === bullet ? "ul" : "ol", null, items.map((t) => h("li", null, inline(t)))));
      continue;
    }
    if (!line.trim()) {
      i++;
      continue;
    }
    const para = [];
    while (i < lines.length && lines[i].trim() && !/^\s*```/.test(lines[i]) && !bullet.test(lines[i]) && !num.test(lines[i])) para.push(lines[i++]);
    out.push(h("p", null, para.map((l, k) => [k ? h("br") : null, inline(l)])));
  }
  return out;
}

function inline(t) {
  const out = [];
  const re = /(`[^`]+`)|(\*\*[^*]+\*\*)|(https:\/\/[^\s)<>]+[^\s)<>.,;:!?'"])/g;
  let last = 0;
  for (let m; (m = re.exec(t)); ) {
    if (m.index > last) out.push(t.slice(last, m.index));
    if (m[1]) out.push(h("code", null, m[1].slice(1, -1)));
    else if (m[2]) out.push(h("strong", null, m[2].slice(2, -2)));
    else out.push(h("a", { href: m[3], target: "_blank", rel: "noopener noreferrer" }, m[3]));
    last = re.lastIndex;
  }
  if (last < t.length) out.push(t.slice(last));
  return out;
}

// A render builds the panes anew. The list keeps its place. So does a conversation being read
// further up; one that is open at its end, or just opened, shows its newest message.
function chatScroll() {
  const thread = root.querySelector(".chat-thread");
  return {
    key: thread?.dataset.conversation,
    top: thread ? thread.scrollTop : 0,
    atEnd: !thread || thread.scrollHeight - thread.scrollTop - thread.clientHeight < 48,
  };
}

function restoreChatScroll(was) {
  const list = root.querySelector(".chat-list");
  const thread = root.querySelector(".chat-thread");
  if (list) list.scrollTop = ui.chatListTop;
  const same = chatMoved || thread?.dataset.conversation === was.key;
  chatMoved = false;
  if (thread) thread.scrollTop = same && !was.atEnd ? was.top : thread.scrollHeight;
}

// Settings.

const WAKE_CHOICES = [
  ["always", "Always"],
  ["within", "Within"],
  ["never", "Never"],
];

// Minutes being typed are saved once the typing pauses. A choice made meanwhile, such as Never,
// cancels them: they belong to the Within it replaced.
const minuteEdits = new Map(); // a control's key → its timer

function cancelMinutes(key) {
  clearTimeout(minuteEdits.get(key));
  minuteEdits.delete(key);
}

function wakeControl(key, wake, onChange, withDefault) {
  const def = ui.settings.defaultMinutes || 3;
  const choices = withDefault ? [["default", "Default"], ...WAKE_CHOICES] : WAKE_CHOICES;
  const current = wake ? wake.when : "default";
  const seg = h(
    "div",
    { class: "segmented", role: "group" },
    choices.map(([v, label]) =>
      h(
        "button",
        {
          "aria-pressed": current === v ? "true" : "false",
          "data-key": key + "-" + v,
          onclick: () => {
            if (v === current) return;
            cancelMinutes(key);
            delete ui.rowErrors[key];
            onChange(v === "default" ? null : { when: v, minutes: v === "within" ? wake?.minutes || 0 : 0 });
          },
        },
        label,
      ),
    ),
  );
  if (current !== "within") return [seg];
  const input = h("input", {
    type: "number",
    min: 1,
    max: 1440,
    step: 1,
    inputmode: "numeric",
    placeholder: String(def),
    "aria-label": "Minutes idle",
    "data-key": key + "-minutes",
    value: wake.minutes ? String(wake.minutes) : null,
    oninput: (e) => {
      cancelMinutes(key);
      const raw = e.target.value.trim();
      const timer = setTimeout(() => {
        minuteEdits.delete(key);
        const n = raw === "" ? 0 : Number(raw);
        if (!Number.isInteger(n) || n < 0 || n > 1440) {
          ui.rowErrors[key] = "Minutes must be a whole number from 1 to 1440.";
          render();
          return;
        }
        delete ui.rowErrors[key];
        onChange({ when: "within", minutes: n });
      }, 600);
      minuteEdits.set(key, timer);
    },
  });
  return [seg, h("span", { class: "field" }, input), h("span", { class: "field-suffix" }, "min")];
}

const BUSY_CHOICES = [
  ["subject", "Subject"],
  ["whole", "Whole message"],
];

function busyControl(key, busy, onChange, withDefault) {
  const choices = withDefault ? [["default", "Default"], ...BUSY_CHOICES] : BUSY_CHOICES;
  const current = busy || "default";
  return h(
    "div",
    { class: "segmented", role: "group" },
    choices.map(([v, label]) =>
      h(
        "button",
        {
          "aria-pressed": current === v ? "true" : "false",
          "data-key": key + "-" + v,
          onclick: () => {
            if (v !== current) onChange(v === "default" ? null : v);
          },
        },
        label,
      ),
    ),
  );
}

// Settings go to the daemon one request at a time, in order: each PUT is the whole settings, so
// one that overtook an older one would be undone by it. Loads queue behind saves too. The page
// shows the newest choice while it is on its way, and only the answer to the newest request is
// shown; if that fails, the page goes back to what the daemon last confirmed.
let settingsQueue = Promise.resolve();
let settingsSeq = 0; // the newest save asked for
let confirmed = null; // the settings as the daemon last gave them

function queueSettings(job) {
  const token = ui.token;
  settingsQueue = settingsQueue
    .then(() => (token === ui.token ? job() : null)) // a new token has started over
    .catch((e) => console.error(e));
}

function saveSettings(next) {
  const seq = ++settingsSeq;
  ui.settings = next;
  render();
  queueSettings(async () => {
    try {
      const saved = await api("/api/settings", { method: "PUT", body: JSON.stringify(next) });
      confirmed = saved;
      if (seq === settingsSeq) {
        ui.settings = saved;
        ui.settingsError = "";
      }
    } catch (e) {
      if (e instanceof AuthError) return deny(e.token);
      if (seq === settingsSeq) {
        ui.settings = confirmed || ui.settings;
        ui.settingsError = e.message;
      }
    }
    render();
  });
}

function settingsRow(key, title, description, control, extraClass = "") {
  return h(
    "div",
    { class: "row" },
    h(
      "div",
      { class: "row-grid " + extraClass },
      h("div", null, h("div", { class: "row-title" }, title), description && h("p", { class: "row-description" }, description)),
      h("div", { class: "row-control" }, control),
    ),
    ui.rowErrors[key] && h("p", { class: "row-error" }, ui.rowErrors[key]),
  );
}

function settingsPage() {
  const sections = [];
  if (ui.settings) {
    const set = ui.settings;
    const local = (ui.state?.devices || []).find((d) => d.self)?.sessions || [];
    const overrides = set.sessionWake || {};
    const rows = [
      settingsRow(
        "wake",
        "Device default",
        "Whether a message from another session may wake an idle session.",
        wakeControl("wake", set.wake, (w) => saveSettings({ ...ui.settings, wake: w || { when: "within" } }), false),
      ),
      ...[...local]
        .sort((a, b) => a.name.localeCompare(b.name))
        .map((s) =>
          settingsRow(
            "wake-" + s.name,
            [s.name, s.agent && h("span", { class: "badge badge-secondary" }, s.agent)],
            null,
            wakeControl(
              "wake-" + s.name,
              overrides[s.name] || null,
              (w) => {
                // From the settings as they are now: a pause in typing minutes can end after
                // another choice was made.
                const next = { ...(ui.settings.sessionWake || {}) };
                if (w) next[s.name] = w;
                else delete next[s.name];
                saveSettings({ ...ui.settings, sessionWake: next });
              },
              true,
            ),
          ),
        ),
    ];
    const busyOverrides = set.sessionBusy || {};
    const busyRows = [
      settingsRow(
        "busy",
        "Device default",
        "What a busy Claude Code session sees when a message arrives.",
        busyControl("busy", set.busy || "subject", (b) => saveSettings({ ...ui.settings, busy: b }), false),
      ),
      ...[...local]
        .sort((a, b) => a.name.localeCompare(b.name))
        .map((s) =>
          settingsRow(
            "busy-" + s.name,
            [s.name, s.agent && h("span", { class: "badge badge-secondary" }, s.agent)],
            null,
            busyControl(
              "busy-" + s.name,
              busyOverrides[s.name] || null,
              (b) => {
                const next = { ...(ui.settings.sessionBusy || {}) };
                if (b) next[s.name] = b;
                else delete next[s.name];
                saveSettings({ ...ui.settings, sessionBusy: next });
              },
              true,
            ),
          ),
        ),
    ];
    sections.push(
      h(
        "section",
        { class: "section" },
        h("div", { class: "section-head" }, h("h2", { class: "section-title" }, "Wake-ups")),
        h("div", { class: "group" }, rows),
      ),
      h(
        "section",
        { class: "section" },
        h("div", { class: "section-head" }, h("h2", { class: "section-title" }, "Busy sessions")),
        h("div", { class: "group" }, busyRows),
        ui.settingsError && h("p", { class: "row-error section-head" }, ui.settingsError),
      ),
    );
  }

  const mutes = ui.state?.mutes || [];
  sections.push(
    h(
      "section",
      { class: "section" },
      h("div", { class: "section-head" }, h("h2", { class: "section-title" }, "Mutes")),
      h(
        "div",
        { class: "group" },
        mutes.length === 0
          ? h("div", { class: "row-empty" }, "None")
          : mutes.map((m) => {
              const key = "mute-" + m.by + "-" + m.muted;
              const at = when(m.at);
              return settingsRow(
                key,
                h(
                  "span",
                  { class: "mute-line" },
                  icon("bellOff"),
                  h("span", { class: "truncate" }, m.by),
                  h("span", { class: "verb" }, "muted"),
                  h("span", { class: "truncate" }, m.muted),
                ),
                at && agoEl("span", "", at),
                m.canUnmute &&
                  h(
                    "button",
                    {
                      class: "btn btn-outline btn-xs",
                      "data-key": key,
                      disabled: ui.muting.has(key),
                      onclick: () => unmute(m, key),
                    },
                    "Unmute",
                  ),
                "keep-inline",
              );
            }),
      ),
    ),
  );

  return h("div", { class: "page" }, sections);
}

async function unmute(m, key) {
  ui.muting.add(key);
  delete ui.rowErrors[key];
  render();
  try {
    await api("/api/unmute", { method: "POST", body: JSON.stringify({ by: m.by, muted: m.muted }) });
    ui.state.mutes = ui.state.mutes.filter((x) => !(x.by === m.by && x.muted === m.muted));
  } catch (e) {
    if (e instanceof AuthError) return deny(e.token);
    ui.rowErrors[key] = e.message;
  }
  ui.muting.delete(key);
  render();
}

// Navigation and polling.

function go(view) {
  if (location.hash !== "#" + view) location.hash = view;
  else setView(view);
}

function setView(view) {
  ui.view = VIEWS.includes(view) ? view : "messages";
  if (ui.view === "settings") loadSettings();
  render();
}

function loadSettings() {
  const seq = settingsSeq;
  queueSettings(async () => {
    try {
      const got = await api("/api/settings");
      confirmed = got;
      if (seq === settingsSeq) {
        ui.settings = got; // unless a save was asked for since, which the page shows instead
        ui.settingsError = "";
      }
    } catch (e) {
      if (e instanceof AuthError) return deny(e.token);
      if (seq === settingsSeq) ui.settingsError = e.message;
    }
    render();
  });
}

// deny gives up on a token the server refused, unless a newer one has replaced it meanwhile.
function deny(token) {
  if (token !== ui.token) return;
  ui.conn = "denied";
  sessionStorage.removeItem(TOKEN_KEY);
  render();
}

let arrivedAt = 0; // the sort time of the message before

async function fetchMessages() {
  for (let pages = 0; pages < 20; pages++) {
    const r = await api("/api/messages?cursor=" + encodeURIComponent(ui.cursor));
    let added = 0;
    for (const m of r.messages || []) {
      learn(m.fromAddr, m.from);
      if (m.toKind === "session") learn(m.toAddr, m.to);
      if (ui.ids.has(m.id)) continue;
      ui.ids.add(m.id);
      // A clock can be wrong. A message with no time goes where it arrived, and one from the future
      // counts as arriving now, so it can't hold its conversation at the top of the list or stay
      // below the replies to it.
      arrivedAt = m.sortAt = Math.min(when(m.at) || arrivedAt, Date.now());
      ui.messages.push(m);
      added++;
    }
    if (ui.messages.length > MAX_MESSAGES) {
      for (const m of ui.messages.splice(0, ui.messages.length - MAX_MESSAGES)) ui.ids.delete(m.id);
    }
    const moved = r.cursor !== ui.cursor;
    ui.cursor = r.cursor || ui.cursor;
    if (!added || !moved) return;
  }
}

let last = "";
let timer;
let inflight = false;
let polled = false;
let scrolledAt = 0;

async function poll() {
  if (inflight) return;
  inflight = true;
  clearTimeout(timer);
  try {
    const [state] = await Promise.all([api("/api/state"), fetchMessages()]);
    for (const d of state.devices || []) for (const s of d.sessions || []) learn(s.addr, s.name);
    ui.state = state;
    ui.conn = "ok";
  } catch (e) {
    inflight = false;
    if (e instanceof AuthError) return deny(e.token);
    ui.conn = "down";
  }
  inflight = false;
  // Rebuild only when something changed, and leave alone a minutes field being typed in, text being
  // selected, and a pane being scrolled: a rebuild would drop the selection or stop the scroll.
  const sig = JSON.stringify([ui.state, ui.cursor, ui.conn]);
  const typing = document.activeElement?.tagName === "INPUT";
  const selecting = document.getSelection()?.isCollapsed === false;
  const scrolling = Date.now() - scrolledAt < 1000;
  if (sig !== last && !typing && !selecting && !scrolling) {
    last = sig;
    render();
  }
  // The first state starts the PR lookups; ask again soon, so their answers show without a wait.
  const first = !polled;
  polled = true;
  timer = setTimeout(poll, document.hidden ? HIDDEN_POLL_MS : first ? 1200 : POLL_MS);
}

// A fresh link pasted into a tab already showing the page changes only the fragment, so the
// page takes the new token here and starts over with it.
function start() {
  const token = takeToken();
  if (token !== ui.token) {
    Object.assign(ui, { token, conn: "connecting", state: null, messages: [], cursor: "", settings: null, conversation: null, chatListTop: 0 });
    ui.ids = new Set();
    named.clear();
    for (const key of [...minuteEdits.keys()]) cancelMinutes(key);
    confirmed = null;
    arrivedAt = 0;
    last = "";
    polled = false;
  }
  ui.view = VIEWS.includes(location.hash.slice(1)) ? location.hash.slice(1) : "messages";
  render();
  if (!ui.token) return;
  loadSettings(); // the wake-up settings say which of this device's sessions show red
  poll();
}

window.addEventListener("hashchange", () => {
  if (location.hash.includes("token=")) start();
  else setView(location.hash.slice(1));
});
document.addEventListener("visibilitychange", () => {
  if (!document.hidden && ui.token && ui.conn !== "denied") poll();
});
document.addEventListener("scroll", () => (scrolledAt = Date.now()), { capture: true, passive: true });
NARROW.addEventListener("change", render);
setInterval(tick, 10000);
start();
