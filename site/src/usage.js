// Counting Murmur's use, and the private page that shows Tyler the counts, with the same numbers as
// markdown and JSON for an agent he gives a key to (#150).
//
// Since 2026-11-08 (#199) the only thing Murmur sends us is a public repo's address, once per repo
// (internal/publicrepo), kept in D1's repos table (migrations/). The daily check-ins of earlier
// versions (#148) are no longer stored; the rows they left in the checkins table are kept as
// history, and the page still shows them.

import statsHtml from './stats.html';
import { prefersMarkdown } from './accept.js';

const RELEASES = 'https://api.github.com/repos/a-Fig/murmur/releases/latest';
const LATEST_KEY = 'https://murmur.afig.dev/_cache/latest-release';
const VERSION = /^\d{1,4}\.\d{1,4}\.\d{1,4}(-[0-9A-Za-z.]{1,20})?$/;
const COOKIE = 'murmur_stats';
// "Active" is a check-in in the last ACTIVE_DAYS days; the goal is Tyler's.
const ACTIVE_DAYS = 7;
const GOAL = 100;
// The day the site stopped storing check-ins (#199). The user numbers come from them, so they end there.
const CHECKINS_ENDED = '2026-11-08';

// POST /v1/checkin: the daily check-in that daemons from before #199 send. None is stored any more
// (Tyler, 2026-11-08), and the body isn't even read. It still answers with the latest release, which
// those daemons show in `mur version`, so they hear of the release that stops them sending it.
export async function checkIn(request, env, ctx) {
  if (request.method !== 'POST') {
    return new Response('Only POST.\n', { status: 405, headers: { Allow: 'POST' } });
  }
  return Response.json({ latest: await latestRelease(ctx) }, { headers: { 'Cache-Control': 'no-store' } });
}

// github.com/<owner>/<name>, as GitHub allows them to be spelled.
const REPO = /^github\.com\/([A-Za-z0-9][A-Za-z0-9-]{0,38})\/([A-Za-z0-9._-]{1,100})$/;

// POST /v1/repo: a public repo that uses Murmur, sent once per repo by internal/publicrepo. Its
// address is the whole request, and all that is kept, with the day it first came. A repo GitHub
// doesn't show to someone signed out is refused, so nothing private or made up is stored.
export async function reportRepo(request, env) {
  if (request.method !== 'POST') {
    return new Response('Only POST.\n', { status: 405, headers: { Allow: 'POST' } });
  }
  const text = await readUpTo(request, 512);
  if (text === null) return new Response('Too long.\n', { status: 413 });
  let r;
  try {
    r = JSON.parse(text);
  } catch {
    return new Response('Not a repo.\n', { status: 400 });
  }
  const m = typeof r?.repo === 'string' ? REPO.exec(r.repo) : null;
  if (!m || m[2] === '.' || m[2] === '..') return new Response('Not a repo.\n', { status: 400 });
  // Stored only once GitHub confirms it public. When GitHub can't say, nothing is stored and the
  // daemon, told to try again, sends it again at its next look.
  const shown = await isPublic(m[1], m[2], env);
  if (shown === false) return new Response('Not a public repo on GitHub.\n', { status: 422 });
  if (shown === null) return new Response("GitHub couldn't confirm it is public; try again later.\n", { status: 503 });
  const day = new Date().toISOString().slice(0, 10);
  // Stored before the answer, so a report that isn't kept fails and the daemon tries again.
  try {
    await env.DB.prepare(
      `INSERT INTO repos (repo, name, first) VALUES (?1, ?2, ?3)
       ON CONFLICT (repo) DO UPDATE SET name = excluded.name`,
    ).bind(r.repo.toLowerCase(), r.repo, day).run();
  } catch (err) {
    console.log(`storing a repo: ${err}`);
    return new Response('Not stored; try again later.\n', { status: 503 });
  }
  return Response.json({ ok: true }, { headers: { 'Cache-Control': 'no-store' } });
}

// Whether GitHub shows owner/name to someone signed out: true, false, or null when GitHub can't say.
// The API answers first. A Worker shares Cloudflare's egress addresses, and others have usually
// spent the API's rate limit for them (#253), so when the API can't say, git's own endpoint on github.com does.
async function isPublic(owner, name, env) {
  return (await publicOnApi(owner, name, env)) ?? (await publicByGit(owner, name));
}

// The API's answer, or null when it can't give one (a rate limit, an outage). GITHUB_TOKEN, if that
// secret is set, only raises the rate limit: a private repo it can see answers private: true, and
// is refused.
async function publicOnApi(owner, name, env) {
  const headers = { 'User-Agent': 'murmur-site', Accept: 'application/vnd.github+json' };
  if (env.GITHUB_TOKEN) headers.Authorization = `Bearer ${env.GITHUB_TOKEN}`;
  try {
    const res = await fetch(`https://api.github.com/repos/${owner}/${name}`, { headers, signal: AbortSignal.timeout(5000) });
    if (res.status === 404) return false;
    if (!res.ok) throw new Error(`GitHub answered ${res.status}`);
    return (await res.json()).private === false;
  } catch (err) {
    console.log(`is ${owner}/${name} public, by the API: ${err}`);
    return null;
  }
}

// What git itself is told, signed out, by github.com/owner/name/info/refs: 200 for a public repo, 401
// for a private or missing one. The repo's own page would do, but the same address is also a topic,
// a product page and more (github.com/topics/javascript answers 200), so it can't say a repo is
// there. A renamed repo redirects to its new name and is followed; the address stored is still the
// one reported, since it leads to a public repo. Anything else is no answer.
async function publicByGit(owner, name) {
  try {
    // Protocol 2, so a big repo answers with its capabilities and not every ref.
    const res = await fetch(`https://github.com/${owner}/${name}/info/refs?service=git-upload-pack`, {
      headers: { 'User-Agent': 'git/murmur-site', 'Git-Protocol': 'version=2' }, signal: AbortSignal.timeout(5000),
    });
    res.body?.cancel();
    if (res.status === 401 || res.status === 404) return false;
    if (res.status !== 200) throw new Error(`GitHub answered ${res.status}`);
    if (res.headers.get('Content-Type') !== 'application/x-git-upload-pack-advertisement') throw new Error('not git');
    return true;
  } catch (err) {
    console.log(`is ${owner}/${name} public, by git: ${err}`);
    return null;
  }
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
  const [series, firsts, current, teams, prev, repos] = await db.batch([
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
    db.prepare(`SELECT name, first FROM repos ORDER BY first DESC, repo`),
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
    checkInsEnded: CHECKINS_ENDED,
    repos: repos.results,
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
  const repos = n.repos.length
    ? table(['Repo', 'First reported'], n.repos.map(r => [`[${r.name}](https://${r.name})`, r.first]))
    : 'None yet.';
  return `# Murmur's active users, ${n.today}

> Check-ins stopped on ${n.checkInsEnded}. The user numbers below are history, from the Murmur versions that sent them. Murmur now sends only a public repo's address, once per repo: those repos are listed at the end.

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

## Public repos using Murmur

${n.repos.length} repos, newest first. Each was reported once, by the first of its clones to see it public.

${repos}

## What the numbers mean

- A **user** is one user account on one machine, known by a random ID, so one person on two machines counts twice.
- A **team** is one repo, known by a hash of its remote's address. No names, repos, messages or paths are sent.
- A daemon checks in at most once a UTC day, and only while one of its sessions is open or for 10 minutes after, so a check-in means someone used Murmur that day. Days are UTC.
- Check-ins began with Murmur 0.2.3 (2026-11-03), and the site stopped storing them on ${n.checkInsEnded}.

The same numbers as JSON: ${origin}/stats/data?days=N (N from 14 to 730, default 90), with the same key. This page takes \`?days=N\` too.
`;
}
