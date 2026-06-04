// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect, beforeEach, afterEach } from "vitest";
import pino from "pino";
import {
  startHttpBridge,
  outcomeToCallbackBody,
  type RunningHttpBridge,
} from "./http-bridge.js";
import {
  startInternalMcpServer,
  type CallbackServerHandle,
} from "./internal-mcp-server.js";
import type { CliRunner } from "./cli-runner.js";
import type { AgentOutcome } from "./agent-run.js";
import { Observability } from "./observability.js";

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

  it("returns 202 + ackId and POSTs Complete in the AsyncCallbackBody success oneof", async () => {
    const res = await fetch(`${bridge.address}/execute`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        node_id: "n-1",
        node_type: "stub-agent",
        attributes: { model: "sonnet" },
        attributes_schema: {},
        callback_url: "http://supervisor.invalid/cb",
      }),
    });
    expect(res.status).toBe(202);
    const body = (await res.json()) as { async_ack_id: string };
    expect(body.async_ack_id).toBeTruthy();

    await waitFor(() => posts.length > 0, 2000);
    expect(posts[0]!.url).toBe("http://supervisor.invalid/cb");
    const cb0 = posts[0]!.body as Record<string, unknown>;
    // AsyncCallbackBody oneof shape the supervisor's parseAsyncCallback
    // requires (success | error | park). The legacy `{type: ...}` / `kind`
    // discriminators are rejected with HTTP 400.
    expect(cb0.type).toBeUndefined();
    expect(cb0.kind).toBeUndefined();
    expect(cb0.async_ack_id).toBe(body.async_ack_id);
    const success = cb0.success as Record<string, unknown>;
    expect(success).toBeDefined();
    // Legacy `result` retired in favour of `attributes_delta`.
    expect(success.attributes_delta).toEqual({ stub: true });
    expect(success.changed).toBe(true);
  });
});

// A present-but-malformed cli.required_signoffs (a host typo'd / forgot the
// public_key) must NOT silently degrade to an ungated run. It must terminal-
// ERROR with agent/attribute_invalid — the same fail-loud failure mode as the
// empty-dispatch_id gate path in agent-run.ts — and never resolve to success.
// parseCliConfig throws a CliConfigError in runAndCallback BEFORE runAgent is
// invoked, so the malformed gate config cannot reach terminal success even in
// stub mode (which would otherwise resolve a stub `success`).
describe("HTTP bridge /execute rejects a malformed sign-off gate config (no silent ungating)", () => {
  let cb: CallbackServerHandle;
  let bridge: RunningHttpBridge;
  const fakeCli: CliRunner = {
    spawn: async () => {
      throw new Error("spawn must not be reached when cli config is malformed");
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

  it("POSTs a terminal agent/attribute_invalid error (not success) when a required_signoffs entry omits public_key", async () => {
    const res = await fetch(`${bridge.address}/execute`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        node_id: "n-gate",
        node_type: "stub-agent",
        dispatch_id: "d-gate",
        attributes: {
          model: "sonnet",
          // Host forgot/typo'd public_key — only `path` present. Silently
          // dropping this entry would yield an empty (⇒ no) gate and let
          // unsigned output through; the parser must reject it instead.
          cli: { required_signoffs: [{ path: "endpoints" }] },
        },
        attributes_schema: {},
        callback_url: "http://supervisor.invalid/cb",
      }),
    });
    // The /execute handler still acks immediately (async handoff); the
    // terminal verdict rides the callback POST.
    expect(res.status).toBe(202);

    await waitFor(() => posts.length > 0, 2000);
    expect(posts).toHaveLength(1);
    const body = posts[0]!.body as Record<string, unknown>;
    // The dispatch must NOT have reached terminal success.
    expect(body.success).toBeUndefined();
    expect(body.error).toBeDefined();
    const error = body.error as { error_class: string };
    expect(error.error_class).toBe("agent/attribute_invalid");
  });

  it("still resolves to success when the gate config is well-formed (control)", async () => {
    const res = await fetch(`${bridge.address}/execute`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        node_id: "n-ok",
        node_type: "stub-agent",
        dispatch_id: "d-ok",
        attributes: {
          model: "sonnet",
          cli: { required_signoffs: [{ public_key: "PEM", path: "endpoints" }] },
        },
        attributes_schema: {},
        callback_url: "http://supervisor.invalid/cb",
      }),
    });
    expect(res.status).toBe(202);
    await waitFor(() => posts.length > 0, 2000);
    const body = posts[0]!.body as Record<string, unknown>;
    // A well-formed gate config parses cleanly — stub run reaches success
    // (the gate itself only enforces inside onComplete, not parsed here).
    expect(body.success).toBeDefined();
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

// HTTP /execute observability ledger coverage. The bridge keys traces
// by the supervisor-supplied dispatch_id; the dashboard fetches via
// GET /observability/v1/trace/{dispatch_id} so the ledger must end up
// in the complete + step_completed shape after a successful run.
describe("HTTP bridge /execute observability ledger", () => {
  let cb: CallbackServerHandle;
  let bridge: RunningHttpBridge;
  let obs: Observability;
  const fakeCli: CliRunner = {
    spawn: async () => {
      throw new Error("should not be called in stub mode");
    },
  };
  const posts: Array<{ url: string; body: unknown }> = [];

  beforeEach(async () => {
    process.env.RIMSKY_EXECUTOR_STUB_MODE = "1";
    posts.length = 0;
    obs = new Observability();
    cb = await startInternalMcpServer({ logger });
    bridge = await startHttpBridge({
      host: "127.0.0.1",
      port: 0,
      callback: cb,
      cliRunner: fakeCli,
      silenceTimeoutMs: 5000,
      logger,
      observability: obs,
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

  it("records step_started + step_completed against dispatch_id and markCompletes the trace", async () => {
    const dispatchId = "bridge-d1";
    const res = await fetch(`${bridge.address}/execute`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        node_id: "n-obs",
        node_type: "stub-agent",
        dispatch_id: dispatchId,
        attributes: { model: "sonnet" },
        attributes_schema: {},
        callback_url: "http://supervisor.invalid/cb",
      }),
    });
    expect(res.status).toBe(202);
    await waitFor(() => posts.length > 0, 2000);

    const trace = obs.getTrace(dispatchId);
    expect(trace.dispatch_id).toBe(dispatchId);
    const cats = trace.events.map((e) => e.category);
    expect(cats).toContain("step_started");
    expect(cats).toContain("step_completed");
    expect(cats).toContain("trace_complete");
    expect(trace.complete).toBe(true);
  });
});

// The HTTP bridge body must also ride the per-dispatch named-event buffer on
// the `events[]` array (the Go supervisor's callback parser reads `events`
// regardless of transport). Payloads are base64-encoded per the proto-JSON
// `bytes` rule.
describe("http-bridge outcomeToCallbackBody named-event surfacing", () => {
  it("populates events[] from the buffer with base64 payloads", () => {
    const payload = Buffer.from(JSON.stringify({ pct: 50 }), "utf8");
    const outcome: AgentOutcome = {
      kind: "complete",
      attributesDelta: { ok: true },
      changed: true,
      changeSummary: "did",
      emittedEvents: [{ name: "progress", payload }],
    };
    const body = outcomeToCallbackBody(outcome, "ack-1");
    expect(body.async_ack_id).toBe("ack-1");
    expect(body.events).toEqual([
      { name: "progress", payload: payload.toString("base64") },
    ]);
    // AsyncCallbackBody oneof shape the supervisor requires (success |
    // error | park) — not the legacy `{type: ...}` discriminator.
    expect(body.success).toBeDefined();
  });

  it("omits events[] entirely when the buffer is empty (no behavior change)", () => {
    const outcome: AgentOutcome = {
      kind: "errored",
      errorClass: "agent/internal_error",
      payload: { error: "boom" },
    };
    const body = outcomeToCallbackBody(outcome, "ack-2");
    expect("events" in body).toBe(false);
    expect(body.error).toBeDefined();
  });
});
