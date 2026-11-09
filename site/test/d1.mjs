// Stand-ins for D1 with the methods the site calls: prepare, bind, first, all, run and batch.
import { readFileSync, readdirSync } from 'node:fs';

const MIGRATIONS = new URL('../migrations/', import.meta.url);

// Keeps no tables: `listed` is the repos table, and each batch is kept in `batches` as its
// statements ({sql, args}), so a test can see what would have been written.
export function recordingD1(listed = []) {
  const repos = new Set(listed.map(r => r.toLowerCase()));
  const batches = [];
  const statement = (sql, args = []) => ({
    sql, args,
    bind: (...a) => statement(sql, a),
    async first() { return /FROM repos WHERE repo = \?1/.test(sql) && repos.has(args[0]) ? { 1: 1 } : null; },
    async all() { return { results: /SELECT name FROM repos/.test(sql) ? listed.map(name => ({ name })) : [] }; },
    async run() { return { success: true }; },
  });
  return {
    batches,
    prepare: sql => statement(sql),
    async batch(stmts) {
      batches.push(stmts);
      return stmts.map(() => ({ results: [] }));
    },
  };
}

// D1 is SQLite, so node's own (node:sqlite, Node 22.5 and later) runs the site's SQL as D1 would,
// on a database made from migrations/. null where node has no SQLite: tests that need it skip.
export async function sqliteD1() {
  let sqlite;
  try {
    sqlite = await import('node:sqlite');
  } catch {
    return null;
  }
  const db = new sqlite.DatabaseSync(':memory:');
  for (const f of readdirSync(MIGRATIONS).filter(f => f.endsWith('.sql')).sort()) db.exec(readFileSync(new URL(f, MIGRATIONS), 'utf8'));
  const statement = (sql, args = []) => ({
    bind: (...a) => statement(sql, a),
    async first() { return db.prepare(sql).get(...args) ?? null; },
    async all() { return { results: db.prepare(sql).all(...args) }; },
    async run() { db.prepare(sql).run(...args); return { success: true }; },
    now() {
      const s = db.prepare(sql);
      if (/^\s*(SELECT|WITH)\b/i.test(sql)) return { results: s.all(...args) };
      s.run(...args);
      return { results: [] };
    },
  });
  return {
    sql: db,
    batches: 0,
    prepare: sql => statement(sql),
    // A batch is one transaction, as D1's is.
    async batch(stmts) {
      this.batches++;
      db.exec('BEGIN');
      try {
        const out = stmts.map(s => s.now());
        db.exec('COMMIT');
        return out;
      } catch (err) {
        db.exec('ROLLBACK');
        throw err;
      }
    },
  };
}
