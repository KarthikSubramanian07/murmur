// Counting active users (#148): the daily check-in every daemon sends (internal/checkin says what
// it sends and how a user turns it off), and the private page that shows Tyler the counts.
// Check-ins are kept in D1 (migrations/), one row per user, team and UTC day.

import statsHtml from './stats.html';

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
  const text = await request.text();
  let c;
  try {
    if (text.length > 1024) throw new Error('too long');
    c = JSON.parse(text);
  } catch {
    return new Response('Not a check-in.\n', { status: 400 });
  }
  if (!HEX32.test(c?.user) || !HEX32.test(c?.team) || !VERSION.test(c?.version) || !WORD.test(c?.os) || !WORD.test(c?.arch)) {
    return new Response('Not a check-in.\n', { status: 400 });
  }
  const day = new Date().toISOString().slice(0, 10);
  ctx.waitUntil(env.DB.prepare(
    `INSERT INTO checkins (day, user, team, version, os, arch) VALUES (?1, ?2, ?3, ?4, ?5, ?6)
     ON CONFLICT (day, user, team) DO UPDATE SET version = excluded.version, os = excluded.os, arch = excluded.arch`,
  ).bind(day, c.user, c.team, c.version, c.os, c.arch).run());
  return Response.json({ latest: await latestRelease(ctx) }, { headers: { 'Cache-Control': 'no-store' } });
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

// /stats, /stats/data and /stats?key=...: the page, its numbers, and the link that lets a browser
// in. The key is the STATS_KEY secret; the link keeps it in a cookie, so it leaves the address bar
// at once. Without the key, as without the secret, the page doesn't exist.
export async function stats(request, env) {
  const url = new URL(request.url);
  const notFound = () => new Response('Not found.\n', { status: 404, headers: { 'X-Robots-Tag': 'noindex' } });
  const key = env.STATS_KEY;
  if (!key) return notFound();
  const given = url.searchParams.get('key');
  if (url.pathname === '/stats' && given !== null) {
    if (!(await same(given, key))) return notFound();
    return new Response(null, {
      status: 303,
      headers: {
        Location: '/stats',
        'Set-Cookie': `${COOKIE}=${encodeURIComponent(key)}; Path=/stats; Max-Age=31536000; HttpOnly; Secure; SameSite=Lax`,
        'Cache-Control': 'no-store',
      },
    });
  }
  const cookie = (request.headers.get('Cookie') || '').split(';').map(s => s.trim()).find(s => s.startsWith(COOKIE + '='));
  if (!cookie || !(await same(decodeURIComponent(cookie.slice(COOKIE.length + 1)), key))) return notFound();
  const headers = { 'Cache-Control': 'no-store', 'X-Robots-Tag': 'noindex' };
  if (url.pathname === '/stats' || url.pathname === '/stats/') {
    return new Response(statsHtml, { headers: { ...headers, 'Content-Type': 'text/html; charset=utf-8' } });
  }
  if (url.pathname === '/stats/data') {
    const days = Math.min(730, Math.max(14, parseInt(url.searchParams.get('days'), 10) || 90));
    return Response.json(await numbers(env.DB, days), { headers });
  }
  return notFound();
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
