import { Hono } from 'hono';
import { stream } from 'hono/streaming';
import type { Context } from 'hono';

import { config } from './config.js';
import { getExecutorEndpoint, getStoreEndpoint } from './discovery.js';

export const proxy = new Hono();

proxy.all('/api/control/*', async (c) => {
  const upstreamPath = c.req.path.replace('/api/control', '/v1/observability');
  const search = new URL(c.req.url).search;
  return forward(c, `${config.controlApiUrl}${upstreamPath}${search}`);
});

proxy.all('/api/exec/:name/*', async (c) => {
  const name = c.req.param('name');
  const ep = await getExecutorEndpoint(name);
  if (!ep) return c.json({ error: { code: 'NOT_FOUND', message: 'unknown executor' } }, 404);
  // Split on the URL parameter, not the resolved endpoint's name —
  // the two are the same today, but discovery could in future return
  // a peer whose `.name` differs from the URL alias and the tail
  // would be wrong.
  const tail = c.req.path.split(`/api/exec/${name}`)[1] ?? '';
  const search = new URL(c.req.url).search;
  return forward(c, `${ep.observabilityHttpUrl}/observability/v1${tail}${search}`);
});

proxy.all('/api/store/:name/*', async (c) => {
  const name = c.req.param('name');
  const ep = await getStoreEndpoint(name);
  if (!ep) return c.json({ error: { code: 'NOT_FOUND', message: 'unknown store' } }, 404);
  // Split on the URL parameter, not the resolved endpoint's name (see
  // the executor-route comment above for the same reason).
  const tail = c.req.path.split(`/api/store/${name}`)[1] ?? '';
  const search = new URL(c.req.url).search;
  return forward(c, `${ep.observabilityHttpUrl}/observability/v1${tail}${search}`);
});

function copyHeaders(src: Headers): Headers {
  const out = new Headers(src);
  out.delete('host');
  out.delete('content-length');
  out.delete('connection');
  return out;
}

async function forward(c: Context, url: string) {
  const accept = c.req.header('Accept') ?? '';
  const method = c.req.method;
  const init: RequestInit = {
    method,
    headers: copyHeaders(c.req.raw.headers),
  };
  if (method !== 'GET' && method !== 'HEAD') {
    init.body = await c.req.raw.arrayBuffer();
  }
  const upstream = await fetch(url, init);
  if (accept.includes('text/event-stream')) {
    // Propagate the SSE headers explicitly so browsers' EventSource
    // parser engages and intermediate caches don't buffer. Honor the
    // upstream's Content-Type when it advertises SSE; otherwise fall
    // back to the canonical text/event-stream headers.
    const upstreamCT = upstream.headers.get('content-type') ?? '';
    c.header(
      'Content-Type',
      upstreamCT.includes('text/event-stream') ? upstreamCT : 'text/event-stream',
    );
    c.header('Cache-Control', upstream.headers.get('cache-control') ?? 'no-cache');
    c.header('Connection', upstream.headers.get('connection') ?? 'keep-alive');
    c.status(upstream.status as Parameters<typeof c.status>[0]);
    return stream(c, async (s) => {
      const reader = upstream.body!.getReader();
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        await s.write(value);
      }
    });
  }
  return new Response(upstream.body, { status: upstream.status, headers: upstream.headers });
}
