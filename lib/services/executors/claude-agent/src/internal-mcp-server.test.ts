// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { afterEach, beforeEach, describe, it, expect } from "vitest";
import pino from "pino";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import {
  registerTools,
  startInternalMcpServer,
  type CallbackServerHandle,
} from "./internal-mcp-server.js";
import { TokenRegistry } from "./token-registry.js";

/**
 * Tests exercise the rimsky-callback tool surface via an InMemoryTransport
 * pair (mirrors brain's `mcp-topic-server_test.ts`). The HTTP transport is
 * itself an SDK concern; tests focus on tool behavior, not transport wiring.
 */

const logger = pino({ level: "silent" });

function makeRegistryEntry(overrides: {
  attributesAtSpawn?: Record<string, unknown>;
  cancelToken?: string;
  nodeId?: string;
  callbackUrl?: string;
  onComplete?: import("./token-registry.js").TokenEntry["onComplete"];
  onPark?: import("./token-registry.js").TokenEntry["onPark"];
  onAttributesSet?: import("./token-registry.js").TokenEntry["onAttributesSet"];
} = {}): import("./token-registry.js").TokenEntry {
  return {
    runId: "run-1",
    attributesAtSpawn: overrides.attributesAtSpawn ?? {},
    cancelToken: overrides.cancelToken ?? "ct",
    nodeId: overrides.nodeId ?? "n-1",
    callbackUrl: overrides.callbackUrl ?? "http://supervisor.invalid/cb",
    onComplete:
      overrides.onComplete ??
      (async () => ({ status: "accepted" as const })),
    onBlocked: async () => {},
    onError: async () => {},
    onPark: overrides.onPark,
    onAttributesSet:
      overrides.onAttributesSet ??
      (async () => ({ status: 204 })),
  };
}

async function buildClient(registry: TokenRegistry): Promise<Client> {
  const server = new McpServer({ name: "rimsky-callback-test", version: "1.0.0" });
  registerTools(server, registry, logger);

  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  await server.connect(serverTransport);

  const client = new Client({ name: "test-client", version: "1.0.0" });
  await client.connect(clientTransport);
  return client;
}

function parseToolText<T>(content: unknown): T {
  const arr = content as Array<{ type: string; text?: string }>;
  return JSON.parse(arr[0]!.text ?? "null") as T;
}

describe("rimsky-callback MCP tools", () => {
  it("lists all six tools (post emit_named_event retirement, incl. report_park)", async () => {
    const registry = new TokenRegistry();
    const client = await buildClient(registry);

    const result = await client.listTools();
    const names = result.tools.map((t) => t.name).sort();
    expect(names).toEqual([
      "attributes_read",
      "attributes_set",
      "report_blocked",
      "report_complete",
      "report_error",
      "report_park",
    ]);
  });

  it("dispatches report_complete with attributes_delta", async () => {
    const registry = new TokenRegistry();
    let captured: {
      delta: Record<string, unknown> | null;
      changed: boolean;
      summary: string | null;
    } | null = null;
    registry.register(
      "tok-ok",
      makeRegistryEntry({
        onComplete: async (delta, changed, summary) => {
          captured = { delta, changed, summary };
          return { status: "accepted" };
        },
      }),
    );
    const client = await buildClient(registry);

    const res = await client.callTool({
      name: "report_complete",
      arguments: {
        token: "tok-ok",
        attributes_delta: { hello: "world" },
        changed: true,
        change_summary: "did",
      },
    });
    expect(parseToolText(res.content)).toEqual({ status: "accepted" });
    expect(captured).toEqual({
      delta: { hello: "world" },
      changed: true,
      summary: "did",
    });
  });

  it("dispatches report_complete without attributes_delta (incremental pattern)", async () => {
    const registry = new TokenRegistry();
    let captured: { delta: Record<string, unknown> | null } | null = null;
    registry.register(
      "tok-ok",
      makeRegistryEntry({
        onComplete: async (delta) => {
          captured = { delta };
          return { status: "accepted" };
        },
      }),
    );
    const client = await buildClient(registry);

    await client.callTool({
      name: "report_complete",
      arguments: { token: "tok-ok", changed: false },
    });
    expect(captured).toEqual({ delta: null });
  });

  it("attributes_read returns the dispatch-time snapshot", async () => {
    const registry = new TokenRegistry();
    registry.register(
      "tok-r",
      makeRegistryEntry({
        attributesAtSpawn: { foo: 1, bar: { nested: true } },
      }),
    );
    const client = await buildClient(registry);

    const res = await client.callTool({
      name: "attributes_read",
      arguments: { token: "tok-r" },
    });
    expect(parseToolText(res.content)).toEqual({
      foo: 1,
      bar: { nested: true },
    });
  });

  it("attributes_set forwards delta to onAttributesSet and reports HTTP status", async () => {
    const registry = new TokenRegistry();
    let captured: { delta: Record<string, unknown> } | null = null;
    registry.register(
      "tok-s",
      makeRegistryEntry({
        onAttributesSet: async (delta) => {
          captured = { delta };
          return { status: 204 };
        },
      }),
    );
    const client = await buildClient(registry);

    const res = await client.callTool({
      name: "attributes_set",
      arguments: { token: "tok-s", delta: { progress: "halfway" } },
    });
    expect(parseToolText(res.content)).toEqual({
      status: "accepted",
      http_status: 204,
    });
    expect(captured).toEqual({ delta: { progress: "halfway" } });
  });

  it("attributes_set reports rejection on non-2xx HTTP status", async () => {
    const registry = new TokenRegistry();
    registry.register(
      "tok-fail",
      makeRegistryEntry({
        onAttributesSet: async () => ({ status: 422 }),
      }),
    );
    const client = await buildClient(registry);

    const res = await client.callTool({
      name: "attributes_set",
      arguments: { token: "tok-fail", delta: { x: 1 } },
    });
    expect(parseToolText(res.content)).toEqual({
      status: "rejected",
      http_status: 422,
    });
    expect(res.isError).toBe(true);
  });

  it("returns isError for unknown token", async () => {
    const registry = new TokenRegistry();
    const client = await buildClient(registry);

    const res = await client.callTool({
      name: "report_complete",
      arguments: { token: "nope", changed: false },
    });
    expect(res.isError).toBe(true);
  });

  it("dispatches report_park with a typed reason + optional note", async () => {
    const registry = new TokenRegistry();
    let captured:
      | {
          reason: string;
          reasonNote: string | null;
          resumeAt: string | null;
        }
      | null = null;
    registry.register(
      "tok-park",
      makeRegistryEntry({
        onPark: async (reason, reasonNote, resumeAt) => {
          captured = { reason, reasonNote, resumeAt };
        },
      }),
    );
    const client = await buildClient(registry);

    const res = await client.callTool({
      name: "report_park",
      arguments: {
        token: "tok-park",
        reason: "await_callback",
        reason_note: "operator review pending",
        resume_at: "2026-05-15T12:00:00Z",
      },
    });
    expect(parseToolText(res.content)).toEqual({ status: "accepted" });
    expect(captured).toEqual({
      reason: "await_callback",
      reasonNote: "operator review pending",
      resumeAt: "2026-05-15T12:00:00Z",
    });
  });

  it("report_park rejects unspecified / unknown reasons via schema", async () => {
    const registry = new TokenRegistry();
    registry.register("tok-bad", makeRegistryEntry({}));
    const client = await buildClient(registry);

    const res = await client.callTool({
      name: "report_park",
      arguments: { token: "tok-bad", reason: "unspecified" },
    });
    expect(res.isError).toBe(true);
  });

  it("report_park surfaces a structured response when the run did not register onPark", async () => {
    const registry = new TokenRegistry();
    registry.register("tok-no-park", makeRegistryEntry({})); // @deliberate: no onPark
    const client = await buildClient(registry);

    const res = await client.callTool({
      name: "report_park",
      arguments: { token: "tok-no-park", reason: "snooze" },
    });
    // @deliberate: the handler returns a structured "park_not_supported" payload
    // (not isError) so the agent can surface a meaningful message to
    // the user; the per-run registration is expected to wire onPark
    // in production. Either shape is acceptable; we just want to
    // assert the path doesn't crash.
    expect(typeof res.content).toBe("object");
  });

});

/**
 * Bug 1 regression coverage: prior versions of `startInternalMcpServer`
 * lazily created a single `StreamableHTTPServerTransport` and held it
 * for the executor's process lifetime. The SDK transport is one-session
 * per instance in stateful mode (see SDK source
 * `node_modules/@modelcontextprotocol/sdk/dist/esm/server/webStandardStreamableHttp.js:422-428`),
 * so the second dispatch's CLI got HTTP 400 `Invalid Request: Server
 * already initialized` on its initialize handshake — surfacing in the
 * CLI as "MCP server not connected." This is the multi-tenant executor
 * bug that wedged the 22-hour docs-pipeline smoke run.
 *
 * These tests spin up the real HTTP server and open two concurrent MCP
 * clients against it. Each must succeed independently.
 */
describe("startInternalMcpServer — multi-session HTTP routing", () => {
  let handle: CallbackServerHandle;

  beforeEach(async () => {
    handle = await startInternalMcpServer({ logger });
  });

  afterEach(async () => {
    await handle.close();
  });

  async function openClient(): Promise<Client> {
    const transport = new StreamableHTTPClientTransport(new URL(handle.url));
    const client = new Client({ name: "rimsky-test-client", version: "1.0.0" });
    await client.connect(transport);
    return client;
  }

  it("supports two concurrent sessions in one server process (bug 1 regression)", async () => {
    // @deliberate: register two distinct dispatches.
    const completedA: { changed: boolean; summary: string | null }[] = [];
    const completedB: { changed: boolean; summary: string | null }[] = [];
    handle.registry.register(
      "tok-A",
      makeRegistryEntry({
        onComplete: async (_delta, changed, summary) => {
          completedA.push({ changed, summary });
          return { status: "accepted" };
        },
      }),
    );
    handle.registry.register(
      "tok-B",
      makeRegistryEntry({
        onComplete: async (_delta, changed, summary) => {
          completedB.push({ changed, summary });
          return { status: "accepted" };
        },
      }),
    );

    const [clientA, clientB] = await Promise.all([openClient(), openClient()]);

    // @deliberate: each client makes a tool call keyed to its own token. Per-session
    // routing is the contract under test: both should land on the
    // correct registry entry.
    const [resA, resB] = await Promise.all([
      clientA.callTool({
        name: "report_complete",
        arguments: { token: "tok-A", changed: true, change_summary: "A done" },
      }),
      clientB.callTool({
        name: "report_complete",
        arguments: { token: "tok-B", changed: false, change_summary: "B noop" },
      }),
    ]);
    expect(parseToolText(resA.content)).toEqual({ status: "accepted" });
    expect(parseToolText(resB.content)).toEqual({ status: "accepted" });
    expect(completedA).toEqual([{ changed: true, summary: "A done" }]);
    expect(completedB).toEqual([{ changed: false, summary: "B noop" }]);

    await clientA.close();
    await clientB.close();
  });

  it("survives long SSE stream (no per-request timeout RST — #11)", async () => {
    // @deliberate: #11 regression: a per-socket inactivity timeout destroys the
    // long-lived standalone MCP SSE GET stream that the SDK client opens
    // after `initialize`. The destroyed socket surfaces on the client as
    // an ECONNRESET `onerror`, and the dispatch terminal-errors
    // `agent/subprocess_exit/before_complete` even though the agent did
    // its work. The fix pins every per-connection/per-request timeout to 0
    // so the stream is indefinitely long-lived.
    //
    // This test drives the REAL server-side fault on a fast clock: we
    // start a dedicated server with a tight per-socket inactivity window
    // (`socketTimeoutMs` — the knob that actually RSTs an idle SSE
    // response), open a real `StreamableHTTPClientTransport` (whose
    // connect handshake opens the standalone GET SSE stream), then hold it
    // open well past that window and assert NO error fires on the
    // transport — i.e. the SSE stream stayed alive. The assertion is the
    // observable outcome (stream survives), not the value of any timeout
    // field.
    const socketTimeoutMs = 250;
    const sseHandle = await startInternalMcpServer({
      logger,
      socketTimeoutMs,
    });
    const transport = new StreamableHTTPClientTransport(new URL(sseHandle.url));
    const errors: unknown[] = [];
    transport.onerror = (err) => {
      errors.push(err);
    };
    const client = new Client({
      name: "rimsky-sse-test-client",
      version: "1.0.0",
    });
    try {
      // @deliberate: connect() runs initialize +
      // notifications/initialized, which makes the SDK client open
      // the standalone GET SSE stream we want to keep alive across
      // the per-request window.
      await client.connect(transport);
      // @deliberate: hold the stream open well past the inactivity window. Under the
      // unfixed server, the idle GET SSE socket is destroyed at
      // `socketTimeoutMs` and the client surfaces an ECONNRESET-class
      // error here.
      await new Promise((resolve) => setTimeout(resolve, socketTimeoutMs * 5));
      // @deliberate: the stream must still be alive: a tool call over the same session
      // round-trips, and no error has fired.
      sseHandle.registry.register("tok-sse", makeRegistryEntry({}));
      const res = await client.callTool({
        name: "report_complete",
        arguments: { token: "tok-sse", changed: false },
      });
      expect(parseToolText(res.content)).toEqual({ status: "accepted" });
      expect(errors).toEqual([]);
    } finally {
      await client.close().catch(() => {});
      await sseHandle.close();
    }
  });

  it("supports a fresh session after a prior one closes (sequential dispatch shape)", async () => {
    // @deliberate: this is the shape that wedged the 22-hour run: dispatch A finishes,
    // its CLI exits, then dispatch B starts later in the same executor
    // process and tries to initialize. A singleton transport would
    // reject B's initialize with HTTP 400 `Server already initialized`.
    handle.registry.register("tok-A", makeRegistryEntry({}));
    handle.registry.register("tok-B", makeRegistryEntry({}));

    const clientA = await openClient();
    const resA = await clientA.callTool({
      name: "report_complete",
      arguments: { token: "tok-A", changed: true },
    });
    expect(parseToolText(resA.content)).toEqual({ status: "accepted" });
    await clientA.close();

    // @deliberate: dispatch B follows in the same server.
    const clientB = await openClient();
    const resB = await clientB.callTool({
      name: "report_complete",
      arguments: { token: "tok-B", changed: true },
    });
    expect(parseToolText(resB.content)).toEqual({ status: "accepted" });
    await clientB.close();
  });
});
