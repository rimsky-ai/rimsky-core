import { describe, it, expect, beforeEach, afterEach } from "vitest";
import pino from "pino";
import { startHttpBridge, type RunningHttpBridge } from "./http-bridge.js";
import {
  startInternalMcpServer,
  type CallbackServerHandle,
} from "./internal-mcp-server.js";
import type { CliRunner } from "./cli-runner.js";

const logger = pino({ level: "silent" });

describe("HTTP bridge stub-mode /execute", () => {
  let cb: CallbackServerHandle;
  let bridge: RunningHttpBridge;
  const fakeCli: CliRunner = {
    spawn: async () => {
      throw new Error("should not be called in stub mode");
    },
  };
  const posts: Array<{ url: string; body: unknown }> = [];

  beforeEach(async () => {
    process.env.RIMSKY_EXECUTOR_STUB_MODE = "1";
    posts.length = 0;
    cb = await startInternalMcpServer({ logger });
    bridge = await startHttpBridge({
      host: "127.0.0.1",
      port: 0,
      callback: cb,
      cliRunner: fakeCli,
      silenceTimeoutMs: 5000,
      logger,
      postCallback: async (url, body) => {
        posts.push({ url, body });
      },
    });
  });

  afterEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    await bridge.shutdown();
    await cb.close();
  });

  it("returns 202 + ackId and POSTs complete outcome", async () => {
    const res = await fetch(`${bridge.address}/execute`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        node_id: "n-1",
        node_type: "stub-agent",
        userdata: { model: "sonnet" },
        callback_url: "http://supervisor.invalid/cb",
      }),
    });
    expect(res.status).toBe(202);
    const body = (await res.json()) as { async_ack_id: string };
    expect(body.async_ack_id).toBeTruthy();

    await waitFor(() => posts.length > 0, 2000);
    expect(posts[0]!.url).toBe("http://supervisor.invalid/cb");
    const cb0 = posts[0]!.body as Record<string, unknown>;
    expect(cb0.kind).toBe("complete");
    expect(cb0.async_ack_id).toBe(body.async_ack_id);
  });
});

async function waitFor(
  fn: () => boolean,
  timeoutMs: number,
): Promise<void> {
  const start = Date.now();
  while (!fn()) {
    if (Date.now() - start > timeoutMs) throw new Error("waitFor: timed out");
    await new Promise((r) => setTimeout(r, 20));
  }
}
