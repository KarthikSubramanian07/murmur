// What a request's Accept header asks for: `/` and `/stats` serve markdown to an agent that asks for
// it, and `/stats` shows a browser an HTML 404.

// Each media range's weight. A range without q= weighs 1, and a malformed q weighs 0.
function weights(accept) {
  const q = {};
  for (const part of (accept || '').split(',')) {
    const [type, ...params] = part.trim().toLowerCase().split(';');
    const qp = params.map(p => p.trim()).find(p => p.startsWith('q='));
    q[type.trim()] = qp ? parseFloat(qp.slice(2)) || 0 : 1;
  }
  return q;
}

// A browser's Accept never names text/markdown, so markdown is served only when it is named and
// ranked at least as high as text/html (ties go to markdown: whoever named it wants it). HTML's
// rank is its own entry's, else text/*'s, else */*'s, as HTTP resolves it: a client that sends
// `text/markdown;q=0.1, text/*` prefers HTML.
export function prefersMarkdown(accept) {
  if (!accept) return false;
  const q = weights(accept);
  const md = q['text/markdown'] ?? 0;
  const html = q['text/html'] ?? q['text/*'] ?? q['*/*'] ?? 0;
  return md > 0 && md >= html;
}

// Whether a request names text/html itself, with a weight above 0, and doesn't prefer markdown:
// what a browser sends when it opens a page. `*/*` alone doesn't count, so curl and fetch tools,
// which send that or nothing, keep getting plain text.
export function wantsHtml(accept) {
  return (weights(accept)['text/html'] ?? 0) > 0 && !prefersMarkdown(accept);
}
