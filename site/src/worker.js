// Static assets answer a Range request with the whole file and a 200, and Safari won't play a video
// served that way. So the videos come through here (run_worker_first in wrangler.jsonc). They are
// under 1 MB, small enough to slice in memory.
export default {
  async fetch(request, env) {
    const res = await env.ASSETS.fetch(request);
    if (!res.ok) return res;
    const headers = new Headers(res.headers);
    headers.set('Accept-Ranges', 'bytes');
    const m = /^bytes=(\d*)-(\d*)$/.exec(request.headers.get('Range') || '');
    if (!m || (!m[1] && !m[2])) return new Response(res.body, { status: res.status, headers });

    const body = await res.arrayBuffer(), size = body.byteLength;
    const start = m[1] ? +m[1] : Math.max(0, size - +m[2]);
    const end = m[1] && m[2] ? Math.min(+m[2], size - 1) : size - 1;
    if (start > end) return new Response(null, { status: 416, headers: { 'Content-Range': `bytes */${size}` } });
    headers.set('Content-Range', `bytes ${start}-${end}/${size}`);
    headers.set('Content-Length', String(end - start + 1));
    return new Response(request.method === 'HEAD' ? null : body.slice(start, end + 1), { status: 206, headers });
  },
};
