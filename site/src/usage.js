// Counting active users (#148): the daily check-in every daemon sends (internal/checkin says what
// it sends and how a user turns it off), and the private page that shows Tyler the counts, with
// the same numbers as markdown and JSON for an agent he gives a key to (#150).
// Check-ins are kept in D1 (migrations/), one row per user, team and UTC day.

import statsHtml from './stats.html';
import { prefersMarkdown } from './accept.js';

const RELEASES = 'https://api.github.com/repos/a-Fig/murmur/releases/latest';
const LATEST_KEY = 'https://murmur.afig.dev/_cache/latest-release';
const HEX32 = /^[0-9a-f]{32}$/;
const VERSION = /^\d{1,4}\.\d{1,4}\.\d{1,4}(-[0-9A-Za-z.]{1,20})?$/;
const WORD = /^[a-z0-9]{1,16}$/;
const COOKIE = 'murmur_stats';
// "Active" is a check-in in the last ACTIVE_DAYS days; the goal is Tyler's.
const ACTIVE_DAYS = 7;
const GOAL = 100;

// POST /v1/checkin: store today's check-in and answer with the latest release.
export async function checkIn(request, env, ctx) {
  if (request.method !== 'POST') {
    return new Response('Only POST.\n', { status: 405, headers: { Allow: 'POST' } });
  }
  const text = await readUpTo(request, 1024);
  if (text === null) return new Response('Too long for a check-in.\n', { status: 413 });
  let c;
  try {
    c = JSON.parse(text);
  } catch {
    return new Response('Not a check-in.\n', { status: 400 });
  }
  if (!HEX32.test(c?.user) || !HEX32.test(c?.team) || !VERSION.test(c?.version) || !WORD.test(c?.os) || !WORD.test(c?.arch)) {
    return new Response('Not a check-in.\n', { status: 400 });
  }
  const day = new Date().toISOString().slice(0, 10);
  // Stored before the answer, so a check-in that isn't kept fails and the daemon tries again.
  try {
    await env.DB.prepare(
      `INSERT INTO checkins (day, user, team, version, os, arch) VALUES (?1, ?2, ?3, ?4, ?5, ?6)
       ON CONFLICT (day, user, team) DO UPDATE SET version = excluded.version, os = excluded.os, arch = excluded.arch`,
    ).bind(day, c.user, c.team, c.version, c.os, c.arch).run();
  } catch (err) {
    console.log(`storing a check-in: ${err}`);
    return new Response('Not stored; try again later.\n', { status: 503 });
  }
  return Response.json({ latest: await latestRelease(ctx) }, { headers: { 'Cache-Control': 'no-store' } });
}

// The body as text, or null if it is longer than max bytes, without reading past max.
async function readUpTo(request, max) {
  if (Number(request.headers.get('Content-Length')) > max) return null;
  if (!request.body) return '';
  const reader = request.body.getReader(), chunks = [];
  let size = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.byteLength;
    if (size > max) {
      await reader.cancel();
      return null;
    }
    chunks.push(value);
  }
  const all = new Uint8Array(size);
  let at = 0;
  for (const c of chunks) {
    all.set(c, at);
    at += c.byteLength;
  }
  return new TextDecoder().decode(all);
}

// The latest release's version, without the v, from GitHub at most once an hour per data center;
// "" if GitHub can't say.
async function latestRelease(ctx) {
  const cache = caches.default;
  const hit = await cache.match(LATEST_KEY);
  if (hit) return hit.text();
  try {
    const res = await fetch(RELEASES, {
      headers: { 'User-Agent': 'murmur-site', Accept: 'application/vnd.github+json' },
      signal: AbortSignal.timeout(5000),
    });
    if (!res.ok) throw new Error(`GitHub answered ${res.status}`);
    const tag = String((await res.json()).tag_name || '').replace(/^v/, '');
    if (!VERSION.test(tag)) throw new Error(`no version in ${JSON.stringify(tag)}`);
    ctx.waitUntil(cache.put(LATEST_KEY, new Response(tag, { headers: { 'Cache-Control': 'max-age=3600' } })));
    return tag;
  } catch (err) {
    console.log(`latest release: ${err}`);
    return '';
  }
}

// The stats, for whoever holds a key: Tyler in a browser, and agents he gives one to (#150).
//
// - /stats: the page in a browser; the report below for anything else, or a browser whose Accept
//   asks for markdown.
// - /stats/report.md: the numbers as markdown, for an agent.
// - /stats/data?days=N: the numbers as JSON, which the page draws.
//
// The keys are the STATS_KEY secret: one or more, separated by spaces or commas, so each reader has
// its own and one can be revoked by putting the secret again without it. A key is taken from a
// `?key=` in the address, an `Authorization: Bearer` header, or the cookie a browser is given: a
// browser that opens /stats?key=... is sent to /stats with the key in a cookie, so it leaves the
// address bar at once. Anything else is answered in place, since a fetch tool may follow a redirect
// without keeping the cookie. Without a key, as without the secret, none of it exists.
export async function stats(request, env) {
  const url = new URL(request.url);
  const notFound = () => new Response('Not found.\n', { status: 404, headers: { 'X-Robots-Tag': 'noindex' } });
  const keys = String(env.STATS_KEY || '').split(/[\s,]+/).filter(Boolean);
  if (keys.length === 0) return notFound();
  const given = url.searchParams.get('key');
  const bearer = /^Bearer\s+(\S+)\s*$/i.exec(request.headers.get('Authorization') || '')?.[1];
  const cookie = (request.headers.get('Cookie') || '').split(';').map(s => s.trim()).find(s => s.startsWith(COOKIE + '='));
  const inCookie = cookie ? safeDecode(cookie.slice(COOKIE.length + 1)) : null;
  const key = given ?? bearer ?? inCookie;
  if (key === null || !(await anyOf(key, keys))) return notFound();

  const page = url.pathname === '/stats' || url.pathname === '/stats/';
  if (page && given !== null && request.headers.get('Sec-Fetch-Mode') === 'navigate') {
    return new Response(null, {
      status: 303,
      headers: {
        Location: '/stats',
        'Set-Cookie': `${COOKIE}=${encodeURIComponent(key)}; Path=/stats; Max-Age=31536000; HttpOnly; Secure; SameSite=Lax`,
        'Cache-Control': 'no-store',
      },
    });
  }
  const headers = { 'Cache-Control': 'no-store', 'X-Robots-Tag': 'noindex' };
  const days = Math.min(730, Math.max(14, parseInt(url.searchParams.get('days'), 10) || 90));
  const fromCookie = given === null && bearer === undefined;
  if (page && fromCookie && !prefersMarkdown(request.headers.get('Accept'))) {
    return new Response(statsHtml, {
      headers: { ...headers, 'Content-Type': 'text/html; charset=utf-8', Link: '</stats/report.md>; rel="alternate"; type="text/markdown"' },
    });
  }
  if (page || url.pathname === '/stats/report.md') {
    const text = report(await numbers(env.DB, days), url.origin);
    return new Response(text, { headers: { ...headers, 'Content-Type': 'text/markdown; charset=utf-8' } });
  }
  if (url.pathname === '/stats/data') return Response.json(await numbers(env.DB, days), { headers });
  return notFound();
}

function safeDecode(s) {
  try {
    return decodeURIComponent(s);
  } catch {
    return null;
  }
}

// Whether key is one of keys, comparing with every one so the time taken doesn't say which.
async function anyOf(key, keys) {
  let found = false;
  for (const k of keys) found = (await same(key, k)) || found;
  return found;
}

async function same(a, b) {
  const enc = new TextEncoder();
  const [x, y] = [enc.encode(a), enc.encode(b)];
  return x.byteLength === y.byteLength && crypto.subtle.timingSafeEqual(x, y);
}

const addDays = (day, n) => new Date(Date.parse(day + 'T00:00:00Z') + n * 864e5).toISOString().slice(0, 10);

// Everything the page shows, for the last `days` days ending today (UTC).
export async function numbers(db, days) {
  const today = new Date().toISOString().slice(0, 10);
  const from = addDays(today, -(days - 1));
  const weekFrom = addDays(today, -(ACTIVE_DAYS - 1));
  const prevFrom = addDays(today, -(2 * ACTIVE_DAYS - 1)), prevTo = addDays(today, -ACTIVE_DAYS);
  const [series, firsts, current, teams, prev] = await db.batch([
    // Per day: users that day, and users in the ACTIVE_DAYS ending that day.
    db.prepare(
      `WITH RECURSIVE days(d) AS (SELECT ?1 UNION ALL SELECT date(d, '+1 day') FROM days WHERE d < ?2)
       SELECT d AS day,
         (SELECT COUNT(DISTINCT user) FROM checkins WHERE day = d) AS dau,
         (SELECT COUNT(DISTINCT user) FROM checkins WHERE day BETWEEN date(d, '-${ACTIVE_DAYS - 1} days') AND d) AS wau,
         (SELECT COUNT(DISTINCT team) FROM checkins WHERE day BETWEEN date(d, '-${ACTIVE_DAYS - 1} days') AND d) AS teams
       FROM days`,
    ).bind(from, today),
    // How many users first checked in on each day.
    db.prepare(`SELECT first AS day, COUNT(*) AS n FROM (SELECT MIN(day) AS first FROM checkins GROUP BY user) GROUP BY first`),
    // Each active user's latest check-in.
    db.prepare(`SELECT user, version, os, arch, MAX(day) AS last FROM checkins WHERE day >= ?1 GROUP BY user`).bind(weekFrom),
    db.prepare(
      `SELECT team, COUNT(DISTINCT CASE WHEN day >= ?1 THEN user END) AS active, COUNT(DISTINCT user) AS users,
         MIN(day) AS first, MAX(day) AS last
       FROM checkins GROUP BY team ORDER BY active DESC, users DESC, last DESC`,
    ).bind(weekFrom),
    db.prepare(`SELECT COUNT(DISTINCT user) AS n FROM checkins WHERE day BETWEEN ?1 AND ?2`).bind(prevFrom, prevTo),
  ]);

  const firstRows = firsts.results;
  const newSince = since => firstRows.filter(r => r.day >= since).reduce((s, r) => s + r.n, 0);
  // New users per ACTIVE_DAYS-long week, the last ending today.
  const weeks = [];
  for (let end = today; end >= from; end = addDays(end, -ACTIVE_DAYS)) {
    const start = addDays(end, -(ACTIVE_DAYS - 1));
    weeks.unshift({ start, end, n: firstRows.filter(r => r.day >= start && r.day <= end).reduce((s, r) => s + r.n, 0) });
  }
  const tally = (rows, f) => {
    const m = new Map();
    for (const r of rows) m.set(f(r), (m.get(f(r)) || 0) + 1);
    return [...m].map(([k, n]) => ({ k, n })).sort((a, b) => b.n - a.n || String(a.k).localeCompare(String(b.k)));
  };
  const active = current.results;
  const activeTeams = teams.results.filter(t => t.active > 0);
  const sizeOf = n => (n >= 6 ? '6+' : n >= 3 ? '3–5' : String(n));
  const sizes = ['1', '2', '3–5', '6+'].map(k => ({ k, n: activeTeams.filter(t => sizeOf(t.active) === k).length }));
  const last = series.results[series.results.length - 1] || { dau: 0, wau: 0, teams: 0 };

  return {
    today, goal: GOAL, activeDays: ACTIVE_DAYS, days,
    active: active.length,
    activeBefore: prev.results[0]?.n ?? 0,
    activeToday: last.dau,
    teams: activeTeams.length,
    newUsers: newSince(weekFrom),
    allUsers: firstRows.reduce((s, r) => s + r.n, 0),
    allTeams: teams.results.length,
    series: series.results,
    newByWeek: weeks,
    versions: tally(active, r => r.version),
    systems: tally(active, r => `${r.os}/${r.arch}`),
    teamSizes: sizes,
    teamList: teams.results.slice(0, 50).map(t => ({ ...t, team: t.team.slice(0, 8) })),
  };
}

// The numbers as markdown, for an agent: what the page shows, in tables, with what they mean.
export function report(n, origin) {
  const pct = Math.round((100 * n.active) / n.goal);
  const diff = n.active - n.activeBefore;
  const change = diff === 0 ? 'the same as' : `${diff > 0 ? 'up' : 'down'} ${Math.abs(diff)} from`;
  const table = (head, rows) =>
    [head, head.map(() => '---'), ...rows].map(r => `| ${r.join(' | ')} |`).join('\n');
  const byDay = new Map(n.series.map(s => [s.day, s]));
  const weeks = [...n.newByWeek].reverse().map(w => {
    const s = byDay.get(w.end) || { wau: 0, teams: 0 };
    return [w.end, s.wau, s.teams, w.n];
  });
  const recent = n.series.slice(-14).reverse().map(s => [s.day, s.dau, s.wau]);
  const tally = (rows, what) => (rows.length ? table([what, 'Active users'], rows.map(r => [r.k, r.n])) : 'None yet.');
  const teams = n.teamList.length
    ? table(['Team', 'Active users', 'Users ever', 'First check-in', 'Last check-in'], n.teamList.map(t => [t.team, t.active, t.users, t.first, t.last]))
    : 'None yet.';
  return `# Murmur's active users, ${n.today}

**${n.active} active users**, ${pct}% of the goal of ${n.goal} (${Math.max(0, n.goal - n.active)} to go), ${change} the ${n.activeDays} days before (${n.activeBefore}).

${table(['Measure', 'Count'], [
    ['Active users (a check-in in the last 7 days)', n.active],
    ['Users today', n.activeToday],
    ['Active teams', n.teams],
    [`New users (first check-in in the last ${n.activeDays} days)`, n.newUsers],
    ['Returning active users', Math.max(0, n.active - n.newUsers)],
    ['Users ever', n.allUsers],
    ['Teams ever', n.allTeams],
  ])}

## By week

Each week is the ${n.activeDays} days ending on the date shown, back over the last ${n.days} days; the newest first.

${table(['Week ending', 'Active users', 'Active teams', 'New users'], weeks)}

## The last 14 days

${table(['Day', 'Users that day', `Active users (${n.activeDays} days ending that day)`], recent)}

## Active users' versions

${tally(n.versions, 'Version')}

## Active users' systems

${tally(n.systems, 'OS/arch')}

## Active teams by size

${table(['Active users', 'Teams'], n.teamSizes.map(s => [s.k, s.n]))}

## Teams

Most active first, at most 50. A team is shown by the first 8 characters of its hash.

${teams}

## What the numbers mean

- A **user** is one user account on one machine, known by a random ID, so one person on two machines counts twice.
- A **team** is one repo, known by a hash of its remote's address. No names, repos, messages or paths are sent.
- A daemon checks in at most once a UTC day, and only while one of its sessions is open or for 10 minutes after, so a check-in means someone used Murmur that day. Days are UTC.
- Check-ins began with Murmur 0.2.3 (2026-11-03); older versions never send one, and a user can turn them off.

The same numbers as JSON: ${origin}/stats/data?days=N (N from 14 to 730, default 90), with the same key. This page takes \`?days=N\` too.
`;
}
