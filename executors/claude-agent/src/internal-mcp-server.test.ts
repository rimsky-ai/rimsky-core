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

describe("startInternalMcpServer", () => {
  it("serves tools/list", async () => {
    handle = await startInternalMcpServer({ logger });
    const res = await fetch(handle.url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }),
    });
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body.result.tools).toHaveLength(3);
  });

  it("dispatches report_complete via tools/call", async () => {
    handle = await startInternalMcpServer({ logger });
    let captured: {
      result: unknown;
      changed: boolean;
      summary: string | null;
    } | null = null;
    handle.registry.register("tok-ok", {
      runId: "run-1",
      resultSchema: {},
      onComplete: async (result, changed, changeSummary) => {
        captured = { result, changed, summary: changeSummary };
        return { status: "accepted" };
      },
      onBlocked: async () => {},
      onError: async () => {},
    });
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
            result: { hello: "world" },
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
      result: { hello: "world" },
      changed: true,
      summary: "did",
    });
  });

  it("returns isError for unknown token", async () => {
    handle = await startInternalMcpServer({ logger });
    const res = await fetch(handle.url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        jsonrpc: "2.0",
        id: 3,
        method: "tools/call",
        params: {
          name: "report_complete",
          arguments: {
            token: "nope",
            result: {},
            changed: false,
          },
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
      body: JSON.stringify({ jsonrpc: "2.0", id: 4, method: "bogus" }),
    });
    const body = await res.json();
    expect(body.error.code).toBe(-32601);
  });
});
