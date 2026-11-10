// What the stats say of the census itself (src/repos.js, health()): when it last ran, whether it is
// late, and which repos that run didn't count. `node --test site/test/report.test.mjs`; it needs
// nothing installed.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { timingSafeEqual } from 'node:crypto';
import { census as handle, repoNumbers, repoReport } from '../src/repos.js';
import { statsD1, sqliteD1 } from './d1.mjs';

// The Workers runtime adds this to Web Crypto; node has it only in node:crypto.
crypto.subtle.timingSafeEqual ??= (a, b) => timingSafeEqual(a, b);

const ago = hours => new Date(Date.now() - hours * 3600e3).toISOString();
const daysAgo = n => new Date(Date.now() - n * 864e5).toISOString().slice(0, 10);
const time = iso => `${iso.slice(0, 16).replace('T', ' ')} UTC`;
// A repo as repoNumbers' first query returns it.
const row = (name, counted) => ({
  repo: name.toLowerCase(), name, reported: daysAgo(5), devices: 1, people: 1, sessions: 1, messages: 1, records: 1,
  start: daysAgo(5), last: daysAgo(1), counted, contributors: 1, week: 1,
});
const report = async repos => {
  const n = await repoNumbers(statsD1(repos), 14);
  return { n, md: repoReport(n, 'https://murmur.invalid') };
};
const section = md => md.slice(md.indexOf('## The census'));

test('a census that counted every repo a few hours ago says so, and no more', async () => {
  const last = ago(2);
  const { n, md } = await report([row('github.com/a/one', last), row('github.com/b/two', ago(2.1)), row('github.com/c/three', ago(2.4))]);
  assert.equal(n.counted, last);
  assert.deepEqual(n.census, { last, hours: 2, late: 0, repos: 3, notCounted: [] });
  assert.equal(section(md), `## The census\n\nIt last ran at ${time(last)}, 2 hours ago, and counted all 3 repos.\n`);
});

test('a census whose newest count is over 30 hours old is late by the hours past a day', async () => {
  const { n, md } = await report([row('github.com/a/one', ago(52.5)), row('github.com/b/two', ago(52.6))]);
  assert.equal(n.census.hours, 52);
  assert.equal(n.census.late, 28);
  assert.match(section(md), /, 52 hours ago, and counted all 2 repos\.\n\nIt runs once a day, so it is \*\*28 hours late\*\*\.\n$/);
  // 30 hours is not yet late: a scheduled run can start hours after its time.
  assert.equal((await report([row('github.com/a/one', ago(29.9))])).n.census.late, 0);
});

test('a repo the last run did not count is listed with when it last was, and one never counted too', async () => {
  const before = ago(27);
  const { n, md } = await report([
    row('github.com/a/one', ago(3)),
    // 29 minutes before the newest count is the same run: a run takes at most its job's 30 minutes.
    row('github.com/b/two', ago(3 + 29 / 60)),
    row('github.com/C/Skipped', before),
    { ...row('github.com/d/new', null), reported: daysAgo(0) },
  ]);
  assert.deepEqual(n.census.notCounted, [
    { name: 'github.com/d/new', counted: null, listed: daysAgo(0) },
    { name: 'github.com/C/Skipped', counted: before, listed: daysAgo(5) },
  ]);
  const s = section(md);
  assert.match(s, /and counted 2 of 4 repos\.\n/);
  assert.doesNotMatch(s, /late/);
  assert.ok(s.includes(`| [d/new](https://github.com/d/new) | never; listed ${daysAgo(0)} |`), s);
  assert.ok(s.includes(`| [C/Skipped](https://github.com/C/Skipped) | ${time(before)} |`), s);
  assert.ok(s.includes('https://github.com/a-Fig/murmur/actions/workflows/census.yml'), s);
});

test('a late census that skipped a repo says both', async () => {
  const { md } = await report([row('github.com/a/one', ago(40)), row('github.com/b/two', ago(70))]);
  const s = section(md);
  assert.match(s, /40 hours ago, and counted 1 of 2 repos\.\n\nIt runs once a day, so it is \*\*16 hours late\*\*\.\n\nNot counted in that run/);
  assert.ok(s.includes('| [b/two](https://github.com/b/two) |'), s);
});

test('before the census has run, the report says it has counted nothing', async () => {
  const { n, md } = await report([row('github.com/a/one', null)]);
  assert.deepEqual(n.census, { last: null, hours: null, late: 0, repos: 1, notCounted: [{ name: 'github.com/a/one', counted: null, listed: daysAgo(5) }] });
  assert.equal(section(md), "## The census\n\nIt hasn't counted any of the 1 repo yet.\n");
  assert.equal(section((await report([])).md), '## The census\n\nIt has no repos to count yet.\n');
});

const sqlite = await sqliteD1();

test("the health comes from repo_census as the census's POSTs leave it", { skip: !sqlite && 'no node:sqlite in this node' }, async () => {
  const db = sqlite, key = 'test-key';
  const post = repo => handle(new Request('https://census.invalid/v1/census', {
    method: 'POST', headers: { Authorization: `Bearer ${key}` },
    body: JSON.stringify({ repo, days: [], devices: [], contributors: [], totals: { sessions: 0, messages: 0, records: 0, first_day: null, last_day: null } }),
  }), { DB: db, CENSUS_KEY: key });
  for (const repo of ['github.com/a/one', 'github.com/a/two']) {
    db.sql.prepare('INSERT INTO repos (repo, name, first) VALUES (?1, ?1, ?2)').run(repo, daysAgo(3));
    assert.equal((await post(repo)).status, 200);
  }
  assert.deepEqual((await repoNumbers(db, 14)).census.notCounted, []);
  // As if a day has passed and the census counted only one of them.
  const then = ago(24);
  db.sql.prepare("UPDATE repo_census SET counted = ?1 WHERE repo = 'github.com/a/two'").run(then);
  const n = await repoNumbers(db, 14);
  assert.deepEqual(n.census.notCounted, [{ name: 'github.com/a/two', counted: then, listed: daysAgo(3) }]);
  assert.equal(n.census.late, 0);
});
