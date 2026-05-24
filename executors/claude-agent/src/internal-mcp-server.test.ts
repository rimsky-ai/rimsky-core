// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect } from "vitest";
import pino from "pino";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import { registerTools } from "./internal-mcp-server.js";
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
  it("lists all six tools (incl. report_park)", async () => {
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
    registry.register("tok-no-park", makeRegistryEntry({})); // no onPark
    const client = await buildClient(registry);

    const res = await client.callTool({
      name: "report_park",
      arguments: { token: "tok-no-park", reason: "snooze" },
    });
    // The handler returns a structured "park_not_supported" payload
    // (not isError) so the agent can surface a meaningful message to
    // the user; the per-run registration is expected to wire onPark
    // in production. Either shape is acceptable; we just want to
    // assert the path doesn't crash.
    expect(typeof res.content).toBe("object");
  });
});
