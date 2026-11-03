// Whether a request's Accept header asks for markdown rather than HTML: `/` and `/stats` serve
// markdown to an agent that does.

// A browser's Accept never names text/markdown, so markdown is served only when it is named and
// ranked at least as high as text/html (ties go to markdown: whoever named it wants it). HTML's
// rank is its own entry's, else text/*'s, else */*'s, as HTTP resolves it: a client that sends
// `text/markdown;q=0.1, text/*` prefers HTML.
export function prefersMarkdown(accept) {
  if (!accept) return false;
  const q = {};
  for (const part of accept.split(',')) {
    const [type, ...params] = part.trim().toLowerCase().split(';');
    const qp = params.map(p => p.trim()).find(p => p.startsWith('q='));
    q[type.trim()] = qp ? parseFloat(qp.slice(2)) || 0 : 1;
  }
  const md = q['text/markdown'] ?? 0;
  const html = q['text/html'] ?? q['text/*'] ?? q['*/*'] ?? 0;
  return md > 0 && md >= html;
}
