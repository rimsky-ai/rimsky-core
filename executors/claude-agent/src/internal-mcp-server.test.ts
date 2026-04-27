import { describe, it, expect, afterEach } from "vitest";
import pino from "pino";
import { startInternalMcpServer, type CallbackServerHandle } from "./internal-mcp-server.js";

const logger = pino({ level: "silent" });

let handle: CallbackServerHandle | null = null;

afterEach(async () => {
  if (handle) {
    await handle.close();
    handle = null;
  }
});

function makeRegistryEntry(overrides: {
  attributesAtSpawn?: Record<string, unknown>;
  cancelToken?: string;
  nodeId?: string;
  callbackUrl?: string;
  onComplete?: import("./token-registry.js").TokenEntry["onComplete"];
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
    onAttributesSet:
      overrides.onAttributesSet ??
      (async () => ({ status: 204 })),
  };
}

describe("startInternalMcpServer", () => {
  it("serves tools/list with all five tools", async () => {
    handle = await startInternalMcpServer({ logger });
    const res = await fetch(handle.url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }),
    });
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body.result.tools).toHaveLength(5);
    const names = body.result.tools
      .map((t: { name: string }) => t.name)
      .sort();
    expect(names).toEqual([
      "attributes_read",
      "attributes_set",
      "report_blocked",
      "report_complete",
      "report_error",
    ]);
  });

  it("dispatches report_complete with attributes_delta", async () => {
    handle = await startInternalMcpServer({ logger });
    let captured: {
      delta: Record<string, unknown> | null;
      changed: boolean;
      summary: string | null;
    } | null = null;
    handle.registry.register(
      "tok-ok",
      makeRegistryEntry({
        onComplete: async (delta, changed, summary) => {
          captured = { delta, changed, summary };
          return { status: "accepted" };
        },
      }),
    );
    const res = await fetch(handle.url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 2,
        method: "tools/call",
        params: {
          name: "report_complete",
          arguments: {
            token: "tok-ok",
            attributes_delta: { hello: "world" },
            changed: true,
            change_summary: "did",
          },
        },
      }),
    });
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body.result.structuredContent).toEqual({ status: "accepted" });
    expect(captured).toEqual({
      delta: { hello: "world" },
      changed: true,
      summary: "did",
    });
  });

  it("dispatches report_complete without attributes_delta (incremental pattern)", async () => {
    handle = await startInternalMcpServer({ logger });
    let captured: { delta: Record<string, unknown> | null } | null = null;
    handle.registry.register(
      "tok-ok",
      makeRegistryEntry({
        onComplete: async (delta) => {
          captured = { delta };
          return { status: "accepted" };
        },
      }),
    );
    const res = await fetch(handle.url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 3,
        method: "tools/call",
        params: {
          name: "report_complete",
          arguments: { token: "tok-ok", changed: false },
        },
      }),
    });
    expect(res.status).toBe(200);
    expect(captured).toEqual({ delta: null });
  });

  it("attributes_read returns the dispatch-time snapshot", async () => {
    handle = await startInternalMcpServer({ logger });
    handle.registry.register(
      "tok-r",
      makeRegistryEntry({
        attributesAtSpawn: { foo: 1, bar: { nested: true } },
      }),
    );
    const res = await fetch(handle.url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 10,
        method: "tools/call",
        params: { name: "attributes_read", arguments: { token: "tok-r" } },
      }),
    });
    const body = await res.json();
    expect(body.result.structuredContent).toEqual({
      attributes: { foo: 1, bar: { nested: true } },
    });
  });

  it("attributes_set forwards delta to onAttributesSet and reports HTTP status", async () => {
    handle = await startInternalMcpServer({ logger });
    let captured: { delta: Record<string, unknown> } | null = null;
    handle.registry.register(
      "tok-s",
      makeRegistryEntry({
        onAttributesSet: async (delta) => {
          captured = { delta };
          return { status: 204 };
        },
      }),
    );
    const res = await fetch(handle.url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 11,
        method: "tools/call",
        params: {
          name: "attributes_set",
          arguments: { token: "tok-s", delta: { progress: "halfway" } },
        },
      }),
    });
    const body = await res.json();
    expect(body.result.structuredContent).toEqual({
      status: "accepted",
      http_status: 204,
    });
    expect(captured).toEqual({ delta: { progress: "halfway" } });
  });

  it("attributes_set reports rejection on non-2xx HTTP status", async () => {
    handle = await startInternalMcpServer({ logger });
    handle.registry.register(
      "tok-fail",
      makeRegistryEntry({
        onAttributesSet: async () => ({ status: 422 }),
      }),
    );
    const res = await fetch(handle.url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 12,
        method: "tools/call",
        params: {
          name: "attributes_set",
          arguments: { token: "tok-fail", delta: { x: 1 } },
        },
      }),
    });
    const body = await res.json();
    expect(body.result.structuredContent).toEqual({
      status: "rejected",
      http_status: 422,
    });
    expect(body.result.isError).toBe(true);
  });

  it("returns isError for unknown token", async () => {
    handle = await startInternalMcpServer({ logger });
    const res = await fetch(handle.url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 4,
        method: "tools/call",
        params: {
          name: "report_complete",
          arguments: { token: "nope", changed: false },
        },
      }),
    });
    const body = await res.json();
    expect(body.result.isError).toBe(true);
  });

  it("rejects unknown method", async () => {
    handle = await startInternalMcpServer({ logger });
    const res = await fetch(handle.url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 5, method: "bogus" }),
    });
    const body = await res.json();
    expect(body.error.code).toBe(-32601);
  });
});
