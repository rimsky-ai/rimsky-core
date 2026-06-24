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
import { TokenRegistry, dispatchContextSnapshot, type DispatchContextSnapshot } from "./token-registry.js";

const logger = pino({ level: "silent" });

function makeRegistryEntry(overrides: {
  attributesAtSpawn?: Record<string, unknown>;
  dispatchContext?: DispatchContextSnapshot;
  cancelToken?: string;
  nodeId?: string;
  callbackUrl?: string;
  onComplete?: import("./token-registry.js").TokenEntry["onComplete"];
  onPark?: import("./token-registry.js").TokenEntry["onPark"];
} = {}): import("./token-registry.js").TokenEntry {
  return {
    runId: "run-1",
    attributesAtSpawn: overrides.attributesAtSpawn ?? {},
    dispatchContext:
      overrides.dispatchContext ??
      dispatchContextSnapshot("d-1", "rs-1", "", ""),
    cancelToken: overrides.cancelToken ?? "ct",
    nodeId: overrides.nodeId ?? "n-1",
    callbackUrl: overrides.callbackUrl ?? "http://supervisor.invalid/cb",
    onComplete:
      overrides.onComplete ??
      (async () => ({ status: "accepted" as const })),
    onBlocked: async () => {},
    onError: async () => {},
    onPark: overrides.onPark,
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
  it("lists the six callback tools (no attributes_set: terminal-bundled writes only)", async () => {
    const registry = new TokenRegistry();
    const client = await buildClient(registry);

    const result = await client.listTools();
    const names = result.tools.map((t) => t.name).sort();
    expect(names).toEqual([
      "attributes_read",
      "dispatch_context_read",
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

  it("dispatch_context_read returns dispatch_id + run_scope_id with no prior on a fresh dispatch", async () => {
    const registry = new TokenRegistry();
    registry.register(
      "tok-fresh",
      makeRegistryEntry({
        dispatchContext: dispatchContextSnapshot("d-fresh", "rs-1", "", ""),
      }),
    );
    const client = await buildClient(registry);
    const res = await client.callTool({
      name: "dispatch_context_read",
      arguments: { token: "tok-fresh" },
    });
    expect(parseToolText(res.content)).toEqual({
      dispatch_id: "d-fresh",
      run_scope_id: "rs-1",
      prior_dispatch_id: null,
      prior_dispatch_disposition: null,
    });
  });

  it("dispatch_context_read maps PRIOR_RETRY_AFTER_ERROR wire enum to retry_after_error", async () => {
    const registry = new TokenRegistry();
    registry.register(
      "tok-retry",
      makeRegistryEntry({
        dispatchContext: dispatchContextSnapshot(
          "d-retry",
          "rs-1",
          "d-prior",
          "PRIOR_RETRY_AFTER_ERROR",
        ),
      }),
    );
    const client = await buildClient(registry);
    const res = await client.callTool({
      name: "dispatch_context_read",
      arguments: { token: "tok-retry" },
    });
    expect(parseToolText(res.content)).toEqual({
      dispatch_id: "d-retry",
      run_scope_id: "rs-1",
      prior_dispatch_id: "d-prior",
      prior_dispatch_disposition: "retry_after_error",
    });
  });

  it("dispatch_context_read maps PRIOR_STALE_RECOVERY wire enum to stale_recovery", async () => {
    const registry = new TokenRegistry();
    registry.register(
      "tok-stale",
      makeRegistryEntry({
        dispatchContext: dispatchContextSnapshot(
          "d-stale",
          "rs-1",
          "d-prior",
          "PRIOR_STALE_RECOVERY",
        ),
      }),
    );
    const client = await buildClient(registry);
    const res = await client.callTool({
      name: "dispatch_context_read",
      arguments: { token: "tok-stale" },
    });
    expect(parseToolText(res.content)).toEqual({
      dispatch_id: "d-stale",
      run_scope_id: "rs-1",
      prior_dispatch_id: "d-prior",
      prior_dispatch_disposition: "stale_recovery",
    });
  });

  it("dispatch_context_read maps PRIOR_RECALCULATE wire enum to recalculate", async () => {
    const registry = new TokenRegistry();
    registry.register(
      "tok-recalc",
      makeRegistryEntry({
        dispatchContext: dispatchContextSnapshot(
          "d-recalc",
          "rs-1",
          "d-prior",
          "PRIOR_RECALCULATE",
        ),
      }),
    );
    const client = await buildClient(registry);
    const res = await client.callTool({
      name: "dispatch_context_read",
      arguments: { token: "tok-recalc" },
    });
    expect(parseToolText(res.content)).toEqual({
      dispatch_id: "d-recalc",
      run_scope_id: "rs-1",
      prior_dispatch_id: "d-prior",
      prior_dispatch_disposition: "recalculate",
    });
  });

  it("dispatch_context_read clears disposition when no prior_dispatch_id is present", async () => {
    const registry = new TokenRegistry();
    registry.register(
      "tok-noprior",
      makeRegistryEntry({
        dispatchContext: dispatchContextSnapshot(
          "d-x",
          "rs-1",
          "",
          "PRIOR_RETRY_AFTER_ERROR",
        ),
      }),
    );
    const client = await buildClient(registry);
    const res = await client.callTool({
      name: "dispatch_context_read",
      arguments: { token: "tok-noprior" },
    });
    expect(parseToolText(res.content)).toEqual({
      dispatch_id: "d-x",
      run_scope_id: "rs-1",
      prior_dispatch_id: null,
      prior_dispatch_disposition: null,
    });
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
    registry.register("tok-no-park", makeRegistryEntry({}));
    const client = await buildClient(registry);

    const res = await client.callTool({
      name: "report_park",
      arguments: { token: "tok-no-park", reason: "snooze" },
    });
    expect(typeof res.content).toBe("object");
  });

});

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
      await client.connect(transport);
      await new Promise((resolve) => setTimeout(resolve, socketTimeoutMs * 5));
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
    handle.registry.register("tok-A", makeRegistryEntry({}));
    handle.registry.register("tok-B", makeRegistryEntry({}));

    const clientA = await openClient();
    const resA = await clientA.callTool({
      name: "report_complete",
      arguments: { token: "tok-A", changed: true },
    });
    expect(parseToolText(resA.content)).toEqual({ status: "accepted" });
    await clientA.close();

    const clientB = await openClient();
    const resB = await clientB.callTool({
      name: "report_complete",
      arguments: { token: "tok-B", changed: true },
    });
    expect(parseToolText(resB.content)).toEqual({ status: "accepted" });
    await clientB.close();
  });
});
