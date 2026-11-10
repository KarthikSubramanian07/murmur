// What a path no page has answers. A browser keeps the static assets' own 404 (an empty body, so it
// shows its usual error page); an agent that asks for markdown gets a short page saying where to go,
// since an empty 404 leaves it guessing.

import { prefersMarkdown } from './accept.js';

const SITE = 'https://murmur.afig.dev';

export const NOT_FOUND_MD = `# Not found

There is no page at this address on murmur.afig.dev, Murmur's website.

Murmur lets coding agents on different teammates' machines, working in the same git repo, see each
other and message each other, over the repo's own git remote. The pages for coding agents:

- [Set Murmur up in a repo](${SITE}/setup): for an agent whose user asked it to set Murmur up
- [Join Murmur](${SITE}/join): for an agent in a repo that already uses Murmur
- [Use Murmur](${SITE}/use): the tools, once joined

An index for agents is at ${SITE}/llms.txt, and every page is listed in ${SITE}/sitemap.xml.
`;

// assetResponse is what the static assets answered; it is returned as it is unless the request
// prefers markdown.
export function notFound(request, assetResponse) {
  if (!prefersMarkdown(request.headers.get('Accept'))) {
    const headers = new Headers(assetResponse.headers);
    headers.append('Vary', 'Accept');
    return new Response(assetResponse.body, { status: assetResponse.status, headers });
  }
  return new Response(request.method === 'HEAD' ? null : NOT_FOUND_MD, {
    status: 404,
    headers: {
      'Content-Type': 'text/markdown; charset=utf-8',
      'Cache-Control': 'public, max-age=300',
      'Vary': 'Accept',
      'X-Robots-Tag': 'noindex',
    },
  });
}
