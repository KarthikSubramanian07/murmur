// The census (site/census/census.mjs) and the site's /v1/census (src/repos.js) together: the census's
// requests go to the Worker's handler, on a D1 stand-in, never to murmur.afig.dev.
// `node --test site/test/census.test.mjs`; it needs git, and nothing installed.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { timingSafeEqual } from 'node:crypto';
import { census as handle } from '../src/repos.js';
import { recordingD1, sqliteD1 } from './d1.mjs';

// The Workers runtime adds this to Web Crypto; node has it only in node:crypto.
crypto.subtle.timingSafeEqual ??= (a, b) => timingSafeEqual(a, b);

process.env.CENSUS_SITE = 'https://census.invalid';
process.env.CENSUS_KEY = 'test-key';
const { main } = await import('../census/census.mjs');

const today = new Date().toISOString().slice(0, 10);
const daysAgo = n => new Date(Date.now() - n * 864e5).toISOString().slice(0, 10);
const ID = 'a'.repeat(16), PERSON = 'b'.repeat(16);

// One repo's counts, as census() makes them: a day of one device, unless `days` says otherwise.
function counts(repo, { days = [{ day: daysAgo(1), sessions: 1, messages: 2, records: 3, devices: [ID] }], contributors = [] } = {}) {
  return {
    repo: repo.toLowerCase(), days,
    devices: [{ id: ID, person: PERSON }],
    totals: { sessions: 1, messages: 2, records: 3, first_day: days[0]?.day ?? null, last_day: days.at(-1)?.day ?? null },
    contributors,
  };
}

// Sends the census's requests to the Worker's handler with this D1, and keeps what it said.
function site(t, db, { down = new Set() } = {}) {
  const env = { DB: db, CENSUS_KEY: process.env.CENSUS_KEY };
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    assert.ok(String(url).startsWith('https://census.invalid/'), String(url));
    const body = init?.body ? JSON.parse(init.body) : null;
    if (body && down.has(body.repo)) throw new TypeError('fetch failed');
    return handle(new Request(url, init), env);
  });
  const said = { log: [], error: [] };
  t.mock.method(console, 'log', s => said.log.push(String(s)));
  t.mock.method(console, 'error', s => said.error.push(String(s)));
  return said;
}

const sent = db => db.batches.map(b => b.find(s => /INTO repo_census/.test(s.sql)).args[0]);

test('a repo the site refuses does not stop the census: the repos after it are still sent, and the job fails', async t => {
  const names = ['github.com/a/one', 'github.com/b/two', 'github.com/c/three'];
  const db = recordingD1(names);
  const said = site(t, db);
  // More days than the site takes: it answers 400.
  const tooLong = Array.from({ length: 3661 }, () => ({ day: daysAgo(1), sessions: 0, messages: 0, records: 0, devices: [] }));
  const read = name => counts(name, name.endsWith('two') ? { days: tooLong } : {});
  await assert.rejects(main(names, { read }), /1 of 3 repos counted but not sent: github\.com\/b\/two/);
  assert.deepEqual(sent(db), ['github.com/a/one', 'github.com/c/three']);
  assert.equal(said.error.length, 1);
  assert.match(said.error[0], /^github\.com\/b\/two: not sent: \/v1\/census: 400 Not counts: days\.$/);
  assert.equal(said.log.length, 2);
});

test('a POST that never reaches the site is one repo not sent, not the end of the census', async t => {
  const names = ['github.com/a/one', 'github.com/b/two'];
  const db = recordingD1(names);
  const said = site(t, db, { down: new Set(['github.com/a/one']) });
  await assert.rejects(main(names, { read: name => counts(name) }), /1 of 2 repos counted but not sent: github\.com\/a\/one/);
  assert.deepEqual(sent(db), ['github.com/b/two']);
  assert.match(said.error[0], /^github\.com\/a\/one: not sent: fetch failed$/);
});

test("a repo that can't be read keeps its counts and the job green, as before", async t => {
  const names = ['github.com/a/one', 'github.com/b/gone', 'github.com/c/three'];
  const db = recordingD1(names);
  const said = site(t, db);
  const read = name => {
    if (name.endsWith('gone')) throw Object.assign(new Error('git failed'), { stderr: 'remote: Repository not found.\n' });
    return counts(name);
  };
  await main(names, { read });
  assert.deepEqual(sent(db), ['github.com/a/one', 'github.com/c/three']);
  assert.deepEqual(said.error, ['github.com/b/gone: not counted: remote: Repository not found.']);
});

test('the job fails when no repo could be read at all', async t => {
  site(t, recordingD1(['github.com/a/one']));
  await assert.rejects(main(['github.com/a/one'], { read: () => { throw new Error('offline'); } }), /no repo could be counted/);
});

test('with no repos named, the census asks the site for its list', async t => {
  const names = ['github.com/A/One', 'github.com/b/two'];
  const db = recordingD1(names);
  site(t, db);
  const asked = [];
  await main([], { read: name => (asked.push(name), counts(name)) });
  assert.deepEqual(asked, names);
  assert.deepEqual(sent(db), ['github.com/a/one', 'github.com/b/two']);
});

// What the site does with one POST.
const post = (db, c) => handle(new Request('https://census.invalid/v1/census', {
  method: 'POST', headers: { Authorization: `Bearer ${process.env.CENSUS_KEY}` }, body: JSON.stringify(c),
}), { DB: db, CENSUS_KEY: process.env.CENSUS_KEY });
const hex = (n, w = 16) => n.toString(16).padStart(w, '0');
const many = n => Array.from({ length: n }, (_, i) => ({ id: hex(i), first: daysAgo(i % 30) }));

test("a big repo's writes go in batches D1 can finish, with its totals last", async () => {
  const db = recordingD1(['github.com/big/repo']);
  const res = await post(db, counts('github.com/big/repo', { contributors: many(40000) }));
  assert.equal(res.status, 200, await res.clone().text());
  // 40,000 contributors in rows of 33 are 1,213 statements, with a day, a device, its day and the totals.
  assert.deepEqual(db.batches.map(b => b.length), [200, 200, 200, 200, 200, 200, 17]);
  const stmts = db.batches.flat();
  const rows = stmts.filter(s => /INTO contributors/.test(s.sql)).reduce((n, s) => n + s.args.length / 3, 0);
  assert.equal(rows, 40000);
  assert.ok(stmts.every(s => s.args.length <= 100), 'D1 binds at most 100 values a statement');
  assert.match(stmts.at(-1).sql, /INTO repo_census/);
  assert.equal(stmts.filter(s => /INTO repo_census/.test(s.sql)).length, 1);
});

test('a POST with more device-days than the site keeps is refused', async () => {
  const db = recordingD1(['github.com/big/repo']);
  const devices = Array.from({ length: 1000 }, (_, i) => hex(i));
  const days = Array.from({ length: 101 }, (_, i) => ({ day: daysAgo(i + 1), sessions: 0, messages: 0, records: 0, devices }));
  const res = await post(db, counts('github.com/big/repo', { days }));
  assert.equal(res.status, 400);
  assert.equal(await res.text(), "Not counts: the days' devices.\n");
  assert.equal(db.batches.length, 0);
});

const sqlite = await sqliteD1();

test('the batched writes land in SQLite as D1 would keep them', { skip: !sqlite && 'no node:sqlite in this node' }, async () => {
  const db = sqlite;
  db.sql.exec(`INSERT INTO repos (repo, name, first) VALUES ('github.com/big/repo', 'github.com/Big/Repo', '${daysAgo(3)}')`);
  const c = counts('github.com/big/repo', { contributors: many(40000) });
  for (let i = 0; i < 2; i++) assert.equal((await post(db, c)).status, 200);
  assert.equal(db.batches, 14);
  const one = sql => db.sql.prepare(sql).get();
  assert.equal(one('SELECT COUNT(*) AS n FROM contributors').n, 40000);
  assert.equal(one('SELECT COUNT(*) AS n FROM device_days').n, 1);
  assert.deepEqual({ ...one('SELECT sessions, messages, records, first_day, last_day FROM repo_census') },
    { sessions: 1, messages: 2, records: 3, first_day: daysAgo(1), last_day: daysAgo(1) });
});
