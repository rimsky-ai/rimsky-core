// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { Hono } from 'hono';
import { stream } from 'hono/streaming';
import type { Context } from 'hono';

import { config } from './config.js';
import { getExecutorEndpoint, getStoreEndpoint, peerHasNoBridge } from './discovery.js';

export const proxy = new Hono();

// PROXY_TIMEOUT_MS bounds the time we wait to *connect* an upstream
// request and (for non-SSE) read the response body. SSE bodies can be
// long-lived; we only apply the timeout to connect, not the body read.
const PROXY_TIMEOUT_MS = Number(process.env.RIMSKY_DASHBOARD_PROXY_TIMEOUT_MS ?? 30_000);

proxy.all('/api/control/*', async (c) => {
  const upstreamPath = c.req.path.replace('/api/control', '/v1/observability');
  const search = new URL(c.req.url).search;
  return forward(c, `${config.controlApiUrl}${upstreamPath}${search}`);
});

proxy.all('/api/exec/:name/*', async (c) => {
  const name = c.req.param('name');
  const ep = await getExecutorEndpoint(name);
  if (!ep) {
    if (await peerHasNoBridge('executor', name)) {
      return c.json(
        {
          error: {
            code: 'BRIDGE_UNAVAILABLE',
            message: `executor ${name} does not expose an HTTP bridge for observability`,
          },
        },
        503,
      );
    }
    return c.json({ error: { code: 'NOT_FOUND', message: 'unknown executor' } }, 404);
  }
  const tail = c.req.path.split(`/api/exec/${name}`)[1] ?? '';
  const search = new URL(c.req.url).search;
  return forward(c, `${ep.observabilityHttpUrl}/observability/v1${tail}${search}`);
});

proxy.all('/api/store/:name/*', async (c) => {
  const name = c.req.param('name');
  const ep = await getStoreEndpoint(name);
  if (!ep) {
    if (await peerHasNoBridge('store', name)) {
      return c.json(
        {
          error: {
            code: 'BRIDGE_UNAVAILABLE',
            message: `store ${name} does not expose an HTTP bridge for observability`,
          },
        },
        503,
      );
    }
    return c.json({ error: { code: 'NOT_FOUND', message: 'unknown store' } }, 404);
  }
  const tail = c.req.path.split(`/api/store/${name}`)[1] ?? '';
  const search = new URL(c.req.url).search;
  return forward(c, `${ep.observabilityHttpUrl}/observability/v1${tail}${search}`);
});

// hopByHop is the RFC 7230 §6.1 hop-by-hop header set; these MUST NOT
// be forwarded between connections. Stripped from both the request
// being sent upstream and the response being relayed back to the
// client (issue 17).
const hopByHop = new Set([
  'connection',
  'keep-alive',
  'proxy-authenticate',
  'proxy-authorization',
  'te',
  'trailer',
  'transfer-encoding',
  'upgrade',
  'host',
  'content-length',
]);

function copyHeaders(src: Headers): Headers {
  const out = new Headers();
  src.forEach((v, k) => {
    if (!hopByHop.has(k.toLowerCase())) out.append(k, v);
  });
  return out;
}

function copyResponseHeaders(src: Headers): Headers {
  const out = new Headers();
  src.forEach((v, k) => {
    if (!hopByHop.has(k.toLowerCase())) out.append(k, v);
  });
  return out;
}

async function forward(c: Context, url: string) {
  const accept = c.req.header('Accept') ?? '';
  const method = c.req.method;
  // AbortController bounds the connect/read time. For SSE we abort
  // only the connect phase; the body read is intentionally long-lived.
  const ac = new AbortController();
  const timeoutHandle = setTimeout(() => ac.abort(), PROXY_TIMEOUT_MS);
  const init: RequestInit = {
    method,
    headers: copyHeaders(c.req.raw.headers),
    signal: ac.signal,
  };
  if (method !== 'GET' && method !== 'HEAD') {
    init.body = await c.req.raw.arrayBuffer();
  }
  let upstream: Response;
  try {
    upstream = await fetch(url, init);
  } catch (err) {
    clearTimeout(timeoutHandle);
    const aborted = (err as { name?: string }).name === 'AbortError';
    return c.json(
      {
        error: {
          code: aborted ? 'UPSTREAM_TIMEOUT' : 'UPSTREAM_UNREACHABLE',
          message: aborted
            ? `upstream ${url} timed out after ${PROXY_TIMEOUT_MS}ms`
            : `upstream ${url} unreachable: ${(err as Error).message}`,
        },
      },
      aborted ? 504 : 502,
    );
  }
  if (accept.includes('text/event-stream')) {
    // SSE path: stop the connect-timeout so the body can stay open.
    clearTimeout(timeoutHandle);
    const upstreamCT = upstream.headers.get('content-type') ?? '';
    c.header(
      'Content-Type',
      upstreamCT.includes('text/event-stream') ? upstreamCT : 'text/event-stream',
    );
    c.header('Cache-Control', upstream.headers.get('cache-control') ?? 'no-cache');
    c.header('X-Accel-Buffering', 'no');
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
  clearTimeout(timeoutHandle);
  return new Response(upstream.body, {
    status: upstream.status,
    headers: copyResponseHeaders(upstream.headers),
  });
}
