// What src/accept.js reads from an Accept header: whether `/` and `/stats` answer an agent in
// markdown, and whether `/stats` answers a missing page with HTML (a browser) or the plain line.
// `node --test site/test/accept.test.mjs`; nothing installed.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { prefersMarkdown, wantsHtml } from '../src/accept.js';

const BROWSER = 'text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8';

test('a browser gets HTML and an agent asking for markdown gets markdown', () => {
  assert.equal(wantsHtml(BROWSER), true);
  assert.equal(prefersMarkdown(BROWSER), false);
  assert.equal(prefersMarkdown('text/markdown'), true);
  assert.equal(wantsHtml('text/markdown'), false);
});

test('curl, fetch tools and API clients keep the plain line', () => {
  for (const accept of [null, '', '*/*', 'application/json', 'text/plain', 'text/*']) {
    assert.equal(wantsHtml(accept), false, `${accept}`);
  }
});

test('HTML refused with q=0 is not served HTML', () => {
  assert.equal(wantsHtml('text/html;q=0, */*'), false);
  assert.equal(wantsHtml('text/html; q=0.0'), false);
  assert.equal(wantsHtml('TEXT/HTML;Q=0.5'), true);
});

test('a client that ranks markdown at least as high as HTML gets markdown, not HTML', () => {
  assert.equal(wantsHtml('text/markdown, text/html;q=0.9'), false);
  assert.equal(wantsHtml('text/markdown, text/html'), false);
  assert.equal(wantsHtml('text/markdown;q=0.5, text/html'), true);
  assert.equal(prefersMarkdown('text/markdown;q=0.1, text/*'), false);
});
