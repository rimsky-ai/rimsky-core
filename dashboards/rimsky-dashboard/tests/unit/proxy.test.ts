// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { proxy } from '../../src/server/proxy';

// fetchSpy mocks global fetch so the proxy talks to a fake upstream.
let fetchSpy: ReturnType<typeof vi.fn>;

beforeEach(() => {
  fetchSpy = vi.fn(async (url: string) => {
    if (url.endsWith('/v1/observability/system/summary')) {
      return new Response(JSON.stringify({ instances_active: 3 }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }
    if (url.endsWith('/v1/observability/executors')) {
      return new Response(
        JSON.stringify({
          executors: [
            { name: 'http-node', http_bridge_url: 'http://http-node:9092' },
          ],
        }),
        { status: 200, headers: { 'content-type': 'application/json' } },
      );
    }
    if (url.endsWith('/v1/observability/stores')) {
      return new Response(JSON.stringify({ stores: [] }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }
    if (url === 'http://http-node:9092/observability/v1/capabilities') {
      return new Response(
        JSON.stringify({ supports_trace_get: true }),
        { status: 200, headers: { 'content-type': 'application/json' } },
      );
    }
    return new Response('not found', { status: 404 });
  });
  vi.stubGlobal('fetch', fetchSpy);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('proxy', () => {
  it('rewrites /api/control/* into /v1/observability/* against control-api', async () => {
    const res = await proxy.request('/api/control/system/summary');
    expect(res.status).toBe(200);
    const body = (await res.json()) as { instances_active: number };
    expect(body.instances_active).toBe(3);
    const calls = fetchSpy.mock.calls.map((c) => c[0]);
    expect(calls.some((u) => String(u).endsWith('/v1/observability/system/summary'))).toBe(true);
  });

  it('routes /api/exec/:name/* through the discovered http_bridge_url', async () => {
    const res = await proxy.request('/api/exec/http-node/capabilities');
    expect(res.status).toBe(200);
    const calls = fetchSpy.mock.calls.map((c) => String(c[0]));
    expect(calls).toContain('http://http-node:9092/observability/v1/capabilities');
  });

  it('returns 404 for unknown executor names', async () => {
    const res = await proxy.request('/api/exec/unknown/capabilities');
    expect(res.status).toBe(404);
  });

  // SSE proxy: when an upstream advertises text/event-stream the
  // dashboard's proxy must forward the canonical SSE headers
  // (Content-Type, Cache-Control, Connection) and propagate the
  // response body and status. Browsers' EventSource only engages on
  // text/event-stream, and intermediate caches must see no-cache so
  // events aren't buffered.
  it('forwards SSE headers and body for text/event-stream upstreams', async () => {
    const sseBody = 'data: {"event_id":"e1"}\n\n';
    fetchSpy.mockImplementation(async (url: string) => {
      if (url === 'http://http-node:9092/observability/v1/trace/d1/stream') {
        const stream = new ReadableStream<Uint8Array>({
          start(controller) {
            controller.enqueue(new TextEncoder().encode(sseBody));
            controller.close();
          },
        });
        return new Response(stream, {
          status: 200,
          headers: {
            'content-type': 'text/event-stream',
            'cache-control': 'no-cache',
            connection: 'keep-alive',
          },
        });
      }
      if (url.endsWith('/v1/observability/executors')) {
        return new Response(
          JSON.stringify({
            executors: [
              { name: 'http-node', http_bridge_url: 'http://http-node:9092' },
            ],
          }),
          { status: 200, headers: { 'content-type': 'application/json' } },
        );
      }
      return new Response('not found', { status: 404 });
    });
    const res = await proxy.request('/api/exec/http-node/trace/d1/stream', {
      method: 'GET',
      headers: { Accept: 'text/event-stream' },
    });
    expect(res.status).toBe(200);
    expect(res.headers.get('Content-Type')).toBe('text/event-stream');
    expect(res.headers.get('Cache-Control')).toBe('no-cache');
    // Hop-by-hop headers (Connection, Transfer-Encoding, Keep-Alive,
    // …) are stripped per RFC 7230 — the proxy no longer forwards
    // them, since hop-by-hop headers are scoped to one TCP connection.
    expect(res.headers.get('Connection')).toBeNull();
    const body = await res.text();
    expect(body).toBe(sseBody);
  });

  // The SSE proxy must propagate non-200 upstream statuses so
  // EventSource clients can see a failed handshake (rather than
  // silently sticking on 200 with no events).
  it('propagates upstream non-200 status on SSE requests', async () => {
    fetchSpy.mockImplementation(async (url: string) => {
      if (url === 'http://http-node:9092/observability/v1/trace/d1/stream') {
        return new Response('upstream gone', {
          status: 503,
          headers: { 'content-type': 'text/event-stream' },
        });
      }
      if (url.endsWith('/v1/observability/executors')) {
        return new Response(
          JSON.stringify({
            executors: [
              { name: 'http-node', http_bridge_url: 'http://http-node:9092' },
            ],
          }),
          { status: 200, headers: { 'content-type': 'application/json' } },
        );
      }
      return new Response('not found', { status: 404 });
    });
    const res = await proxy.request('/api/exec/http-node/trace/d1/stream', {
      method: 'GET',
      headers: { Accept: 'text/event-stream' },
    });
    expect(res.status).toBe(503);
  });
});
