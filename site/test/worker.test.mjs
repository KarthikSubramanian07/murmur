// The Worker's answers to a path no page has, and that the videos still go through it.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import worker from '../src/worker.js';
import { NOT_FOUND_MD } from '../src/notfound.js';

// The static assets as the Worker sees them: the files named here, and an empty 404 for the rest.
function assets(files) {
  return {
    async fetch(request) {
      const { pathname } = new URL(request.url);
      if (pathname in files) return new Response(files[pathname], { status: 200, headers: { 'Content-Type': 'video/mp4' } });
      return new Response(null, { status: 404 });
    },
  };
}

const env = { ASSETS: assets({ '/how-wide.mp4': '0123456789' }) };
const get = (path, headers = {}, method = 'GET') => worker.fetch(new Request(`https://murmur.afig.dev${path}`, { method, headers }), env, {});

test('an agent that asks for markdown gets a markdown 404', async () => {
  for (const accept of ['text/markdown', 'text/markdown, text/html;q=0.9', 'text/markdown, */*']) {
    const res = await get('/__no-such-page', { Accept: accept });
    assert.equal(res.status, 404, accept);
    assert.equal(res.headers.get('Content-Type'), 'text/markdown; charset=utf-8');
    assert.match(res.headers.get('Vary'), /Accept/);
    assert.equal(res.headers.get('X-Robots-Tag'), 'noindex');
    const body = await res.text();
    assert.equal(body, NOT_FOUND_MD);
  }
});

test('the markdown 404 says what happened and where to go', () => {
  assert.ok(NOT_FOUND_MD.length >= 20);
  assert.match(NOT_FOUND_MD, /^# Not found\n/);
  for (const link of ['https://murmur.afig.dev/llms.txt', 'https://murmur.afig.dev/sitemap.xml', 'https://murmur.afig.dev/setup', 'https://murmur.afig.dev/join', 'https://murmur.afig.dev/use']) {
    assert.ok(NOT_FOUND_MD.includes(link), link);
  }
});

test('a HEAD for markdown gets the 404 without a body', async () => {
  const res = await get('/__no-such-page', { Accept: 'text/markdown' }, 'HEAD');
  assert.equal(res.status, 404);
  assert.equal(res.headers.get('Content-Type'), 'text/markdown; charset=utf-8');
  assert.equal(await res.text(), '');
});

test('a browser, or a client that names nothing, keeps the assets\' empty 404', async () => {
  for (const accept of [undefined, '*/*', 'text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8', 'text/markdown;q=0.1, text/*']) {
    const res = await get('/__no-such-page', accept ? { Accept: accept } : {});
    assert.equal(res.status, 404, String(accept));
    assert.equal(res.headers.get('Content-Type'), null, String(accept));
    assert.equal(await res.text(), '', String(accept));
    assert.match(res.headers.get('Vary'), /Accept/);
  }
});

test('the videos are still sliced for a Range request', async () => {
  const res = await get('/how-wide.mp4', { Range: 'bytes=2-5' });
  assert.equal(res.status, 206);
  assert.equal(res.headers.get('Content-Range'), 'bytes 2-5/10');
  assert.equal(await res.text(), '2345');
});

test('a missing video is a 404 as before, markdown or not', async () => {
  assert.equal((await get('/missing.mp4')).status, 404);
  assert.equal((await get('/missing.mp4', { Accept: 'text/markdown' })).status, 404);
});
