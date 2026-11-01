// The page. Plain DOM, no framework and no build step: the daemon embeds these files as they
// are. Everything from the bus is untrusted text, so it only ever reaches the page as text nodes.
import { icon } from "./icons.js";

const NAME = document.querySelector('meta[name="app-name"]').content;
const TOKEN_KEY = NAME + ":token";
const POLL_MS = 3000;
const HIDDEN_POLL_MS = 15000;
const MAX_MESSAGES = 2000;
const VIEWS = ["sessions", "messages", "settings"];
const TITLES = { sessions: "Sessions", messages: "Messages", settings: "Settings" };
const MODE_KEY = NAME + ":messages";
const NARROW = window.matchMedia("(max-width: 767px)"); // style.css's mobile width

const ui = {
  view: "sessions",
  token: null,
  conn: "connecting", // connecting, ok, down, denied
  state: null,
  messages: [], // oldest first
  ids: new Set(),
  cursor: "",
  settings: null,
  settingsError: "",
  rowErrors: {},
  expanded: new Set(),
  filter: null, // a session name
  messageMode: localStorage.getItem(MODE_KEY) === "conversations" ? "conversations" : "recent",
  conversation: null, // the open conversation's key
  conversationAnchor: null, // a message in it
  chatListTop: 0,
  muting: new Set(),
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
    const view = parts.find((p) => VIEWS.includes(p)) || "sessions";
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

function learn(addr, name) {
  if (addr && name && name !== addr) named.set(addr, name);
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
  if (!content) content = h("div", { class: "content" });
  const inner = page(); // before the top bar, which counts what this settles
  content.className = "content" + (inner.classList.contains("chat-page") ? " content-chat" : "");
  content.replaceChildren(inner);
  root.replaceChildren(h("div", { class: "app" }, sidebar(), h("main", { class: "main" }, topbar(), content)));
  content.scrollTop = scroll;
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

function sidebar() {
  const devs = devices();
  return h(
    "aside",
    { class: "sidebar", "data-app-sidebar": true, "aria-label": NAME },
    h("div", { class: "sidebar-header" }, h("span", { class: "brand" }, NAME)),
    h(
      "div",
      { class: "sidebar-content" },
      h(
        "div",
        { class: "sidebar-group" },
        h(
          "ul",
          { class: "sidebar-menu" },
          navButton("sessions", "users", ui.state ? sessionCount() : null),
          navButton("messages", "message", ui.messages.length || null),
          navButton("settings", "settings", null),
        ),
      ),
      devs.length > 0 &&
        h(
          "div",
          { class: "sidebar-group" },
          h("div", { class: "sidebar-label" }, "Devices"),
          h(
            "ul",
            { class: "sidebar-menu" },
            devs.map((d) =>
              h(
                "li",
                null,
                h(
                  "button",
                  {
                    class: "device-link" + (d.reachable ? "" : " stale"),
                    title: d.reachable ? d.id : d.id + ", last seen " + ago(when(d.lastSeen) || 0),
                    "data-key": "dev-" + d.id,
                    onclick: () => showDevice(d.id),
                  },
                  h("span", { class: "dot dot-sm" + (d.reachable ? " ok" : "") }),
                  h("span", { class: "label" }, d.id),
                  h("span", { class: "menu-count" }, d.sessions.length),
                ),
              ),
            ),
          ),
        ),
    ),
  );
}

function topbar() {
  let meta = "";
  if (ui.conn === "down") {
    meta = h("span", { class: "device-state" }, h("span", { class: "dot warn" }), "Disconnected");
  } else if (ui.view === "sessions" && ui.state) {
    meta = plural(sessionCount(), "session") + " · " + plural(ui.state.devices.length, "device");
  } else if (ui.view === "messages") {
    meta =
      ui.messageMode === "conversations"
        ? plural(conversations().length, "conversation")
        : plural(visibleMessages().length, "message");
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
          { "aria-pressed": ui.view === v ? "true" : "false", "data-key": "m-" + v, onclick: () => go(v) },
          TITLES[v],
        ),
      ),
    ),
    meta && h("div", { class: "topbar-meta" }, meta),
  );
}

function page() {
  if (ui.view === "messages") return messagesPage();
  if (ui.view === "settings") return settingsPage();
  return sessionsPage();
}

// Sessions, grouped by device.

function sessionsPage() {
  if (!ui.state) return h("div", { class: "page" });
  const devs = devices();
  if (devs.length === 0) return h("div", { class: "blank" }, "No sessions");
  return h(
    "div",
    { class: "page" },
    devs.map((d) =>
      h(
        "section",
        { class: "section", id: "device-" + d.id },
        h(
          "div",
          { class: "device-head" },
          icon("laptop"),
          h("span", { class: "device-name truncate" }, d.id),
          d.owner && h("span", { class: "device-id truncate" }, d.owner),
          d.self && h("span", { class: "badge badge-outline" }, "This device"),
          !d.reachable &&
            h(
              "span",
              { class: "device-state" },
              h("span", { class: "dot" }),
              when(d.lastSeen) ? agoEl("span", "", when(d.lastSeen), "Unreachable · seen ") : "Unreachable",
            ),
        ),
        h(
          "div",
          { class: "group" },
          d.sessions.length === 0
            ? h("div", { class: "row-empty" }, "No sessions")
            : d.sessions.map((s) => sessionRow(d, s)),
        ),
      ),
    ),
  );
}

function prLink(pr) {
  const draft = pr.draft;
  const label = "#" + pr.number;
  const props = {
    class: "pr " + (draft ? "pr-draft" : "pr-open"),
    title: (draft ? "Draft pull request " : "Open pull request ") + label,
  };
  const kids = [icon(draft ? "prDraft" : "pr"), draft ? "Draft " + label : label];
  if (!/^https:\/\//.test(pr.url || "")) return h("span", props, kids);
  return h("a", { ...props, href: pr.url, target: "_blank", rel: "noopener noreferrer", onclick: (e) => e.stopPropagation() }, kids);
}

function sessionRow(d, s) {
  const active = when(s.lastActive);
  const started = when(s.started);
  const committed = Array.isArray(s.committed) ? s.committed : [];
  // Own keys only: a branch named constructor or __proto__ would otherwise find Object's.
  const prs = ui.state.prs || {};
  const pr = s.branch && Object.hasOwn(prs, s.branch) ? prs[s.branch] : null;
  const open = () => {
    ui.filter = s.addr || s.name;
    ui.conversation = null;
    ui.chatListTop = 0;
    go("messages");
  };
  return h(
    "div",
    {
      class: "session" + (d.reachable ? "" : " unreachable"),
      tabindex: 0,
      role: "button",
      "data-key": "s-" + s.name,
      "aria-label": s.name + ", messages",
      onclick: open,
      onkeydown: (e) => {
        // Only keys aimed at the row: Enter on the PR link inside it opens the link.
        if (e.target !== e.currentTarget) return;
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          open();
        }
      },
    },
    h(
      "div",
      { class: "session-line" },
      h("span", { class: "dot" + (d.reachable ? " ok" : ""), title: d.reachable ? "Reachable" : "Unreachable" }),
      h("span", { class: "session-name truncate" }, s.name),
      s.agent && h("span", { class: "badge badge-secondary" }, s.agent),
      active && agoEl("span", "session-active", active, "active "),
    ),
    s.doing && h("div", { class: "session-doing truncate", title: s.doing }, s.doing),
    h(
      "div",
      { class: "session-meta" },
      s.branch &&
        h("span", { class: "meta-item", title: s.branch }, icon("branch"), h("span", { class: "truncate" }, s.branch)),
      pr && h("span", { class: "meta-item" }, prLink(pr)),
      // Shown only when short: a longer list would crowd the row, and `lookup` has it whole.
      committed.length > 0 &&
        committed.length <= 3 &&
        h("span", { class: "meta-item", title: "Committed to " + committed.join(", ") }, "committed to " + committed.join(", ")),
      s.worktree &&
        h("span", { class: "meta-item", title: "Worktree" }, icon("folder"), h("span", { class: "truncate" }, s.worktree)),
      started && agoEl("span", "meta-item tabular", started, "started "),
    ),
  );
}

function showDevice(id) {
  if (ui.view !== "sessions") go("sessions");
  requestAnimationFrame(() => document.getElementById("device-" + id)?.scrollIntoView({ block: "start" }));
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

function recipient(m) {
  if (m.toKind === "all") return h("span", { class: "msg-to" }, icon("broadcast"), "Everyone");
  if (m.toKind === "device") return h("span", { class: "msg-to", title: "Every session on " + m.to }, icon("laptop"), h("span", { class: "truncate" }, m.to));
  const to = toName(m);
  return h("span", { class: "msg-to", title: to }, h("span", { class: "truncate" }, to));
}

function messagesBar() {
  const shown = ui.filter && nameOf(ui.filter, ui.filter);
  return h(
    "div",
    { class: "filter-bar" },
    ui.filter &&
      h(
        "span",
        { class: "chip" },
        shown,
        h("button", { "aria-label": "Show all messages", "data-key": "unfilter", onclick: () => ((ui.filter = null), render()) }, "×"),
      ),
    h(
      "div",
      { class: "segmented msg-modes", role: "group", "aria-label": "Message view" },
      [
        ["recent", "Recent"],
        ["conversations", "Conversations"],
      ].map(([mode, label]) =>
        h(
          "button",
          {
            "aria-pressed": ui.messageMode === mode ? "true" : "false",
            "data-key": "mode-" + mode,
            onclick: () => {
              ui.messageMode = mode;
              localStorage.setItem(MODE_KEY, mode);
              render();
            },
          },
          label,
        ),
      ),
    ),
  );
}

function messagesPage() {
  if (ui.messageMode === "conversations") return conversationsPage();
  const list = [...visibleMessages()].reverse();
  if (list.length === 0) {
    return h("div", { class: "page" }, messagesBar(), h("div", { class: "blank" }, noMessages()));
  }
  return h(
    "div",
    { class: "page" },
    messagesBar(),
    h(
      "div",
      { class: "group msg-list" },
      list.map((m) => messageRow(m)),
    ),
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

function conversationIcon(c) {
  if (c.kind === "all") return h("span", { class: "avatar" }, icon("broadcast"));
  if (c.kind === "device") return h("span", { class: "avatar" }, icon("laptop"));
  if (c.pair.length === 1) return avatar(c.pair[0]);
  return h("span", { class: "avatar-pair" }, c.pair.map(avatar));
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
    messagesBar(),
    list.length === 0
      ? h("div", { class: "blank" }, noMessages())
      : h(
          "div",
          { class: "chat" + (open ? " chat-open" : "") },
          h(
            "nav",
            { class: "chat-list", "aria-label": "Conversations", onscroll: (e) => (ui.chatListTop = e.target.scrollTop) },
            list.map(conversationRow),
          ),
          open && conversationPane(open),
        ),
  );
}

function conversationRow(c) {
  const last = c.messages[c.messages.length - 1];
  const at = when(last.at);
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
    conversationIcon(c),
    h(
      "span",
      { class: "chat-row-text" },
      h(
        "span",
        { class: "chat-row-line" },
        h("span", { class: "chat-row-title truncate", title: c.title }, c.title),
        at && agoEl("span", "chat-row-time", at),
      ),
      h("span", { class: "chat-row-preview truncate" }, fromName(last) + ": " + last.subject),
    ),
  );
}

function conversationPane(c) {
  const senders = new Set(c.messages.map(fromName));
  const about = [
    c.kind === "all" && "Every session",
    c.kind === "device" && "Every session on " + c.title,
    plural(c.messages.length, "message"),
    c.kind !== "dm" && plural(senders.size, "sender"),
  ]
    .filter(Boolean)
    .join(" · ");
  return h(
    "section",
    { class: "chat-pane", "aria-label": c.title },
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
      conversationIcon(c),
      h(
        "div",
        { class: "chat-head-text" },
        h("h2", { class: "chat-title truncate", title: c.title }, c.title),
        h("div", { class: "chat-about truncate", title: [...senders].join(", ") }, about),
      ),
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
  return h(
    "article",
    { class: "chat-msg" + (head ? " chat-msg-first" : ""), title: head ? null : messageDetail(m) },
    head ? avatar(from) : h("span", { class: "avatar-gap" }),
    h(
      "div",
      { class: "chat-msg-main" },
      head &&
        h(
          "div",
          { class: "chat-msg-head" },
          h("span", { class: "chat-msg-from truncate" }, from),
          h(
            "span",
            { class: "chat-msg-time", title: messageDetail(m) },
            at && new Date(at).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" }),
          ),
        ),
      h("div", { class: "chat-msg-subject" }, m.subject),
      m.body && h("div", { class: "chat-msg-body" }, m.body),
    ),
  );
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

function messageRow(m) {
  const open = ui.expanded.has(m.id);
  const at = when(m.at);
  const from = fromName(m);
  const toggle = () => {
    if (ui.expanded.has(m.id)) ui.expanded.delete(m.id);
    else ui.expanded.add(m.id);
    render();
  };
  const detail = messageDetail(m);
  return h(
    "div",
    { class: "msg", "aria-expanded": open ? "true" : "false" },
    h(
      "button",
      { class: "msg-head", "data-key": "m-" + m.id, "aria-expanded": open ? "true" : "false", onclick: toggle },
      icon("chevronRight", "msg-chev"),
      h("span", { class: "msg-from truncate", title: from }, from),
      icon("arrowRight", "arrow"),
      recipient(m),
      h("span", { class: "msg-subject", title: m.subject }, m.subject),
      at ? agoEl("span", "msg-time", at) : h("span", { class: "msg-time" }),
    ),
    open && detail && h("div", { class: "msg-detail" }, detail),
    open && m.body && h("div", { class: "msg-body" }, m.body),
  );
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
    sections.push(
      h(
        "section",
        { class: "section" },
        h("div", { class: "section-head" }, h("h2", { class: "section-title" }, "Wake-ups")),
        h("div", { class: "group" }, rows),
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

  const theme = window.appearance.get();
  sections.push(
    h(
      "section",
      { class: "section" },
      h("div", { class: "section-head" }, h("h2", { class: "section-title" }, "Appearance")),
      h(
        "div",
        { class: "group" },
        settingsRow(
          "theme",
          "Theme",
          null,
          h(
            "div",
            { class: "segmented", role: "group" },
            [
              ["system", "System"],
              ["light", "Light"],
              ["dark", "Dark"],
            ].map(([v, label]) =>
              h(
                "button",
                {
                  "aria-pressed": theme === v ? "true" : "false",
                  "data-key": "theme-" + v,
                  onclick: () => {
                    window.appearance.set(v);
                    render();
                  },
                },
                label,
              ),
            ),
          ),
          "keep-inline",
        ),
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
  ui.view = VIEWS.includes(view) ? view : "sessions";
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
  ui.view = VIEWS.includes(location.hash.slice(1)) ? location.hash.slice(1) : "sessions";
  render();
  if (!ui.token) return;
  if (ui.view === "settings") loadSettings();
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
