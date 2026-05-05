// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect, beforeEach, afterEach } from "vitest";
import * as grpc from "@grpc/grpc-js";
import pino from "pino";
import * as http from "node:http";
import type { AddressInfo } from "node:net";
import { loadNodeExecutorProto } from "./proto-loader.js";
import { startGrpcServer, type RunningServer } from "./server.js";
import {
  startInternalMcpServer,
  type CallbackServerHandle,
} from "./internal-mcp-server.js";
import type { CliRunner } from "./cli-runner.js";
import { Observability } from "./observability.js";

const logger = pino({ level: "silent" });

interface ExecuteEvent {
  heartbeat?: { timestamp_ms: number; note: string };
  async_accepted?: { async_ack_id: string; expected_completion_ms: number };
  complete?: {
    attributes_delta: unknown;
    changed: boolean;
    change_summary: string;
  };
  blocked?: { reason: string };
  errored?: { error_class: string };
}

describe("gRPC server stub-mode Execute end-to-end", () => {
  let cb: CallbackServerHandle;
  let srv: RunningServer;
  const fakeCli: CliRunner = {
    spawn: async () => {
      throw new Error("should not be called in stub mode");
    },
  };

  // Capture calls made via the supervisor callback URL (mocked).
  const callbackPosts: Array<{ url: string; body: unknown }> = [];
  const fakeCallbackUrl = "http://supervisor.invalid/rimsky/callback";

  beforeEach(async () => {
    process.env.RIMSKY_EXECUTOR_STUB_MODE = "1";
    callbackPosts.length = 0;
    cb = await startInternalMcpServer({ logger });
    srv = await startGrpcServer({
      host: "127.0.0.1",
      port: 0,
      callback: cb,
      cliRunner: fakeCli,
      silenceTimeoutMs: 5000,
      logger,
      postCallback: async (url, body) => {
        callbackPosts.push({ url, body });
      },
    });
  });

  afterEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    await srv.shutdown();
    await cb.close();
  });

  it("emits Heartbeat + AsyncAccepted, then POSTs Complete outcome with attributes_delta", async () => {
    const pkg = loadNodeExecutorProto();
    const Client = pkg.rimsky.v1.NodeExecutor as unknown as new (
      addr: string,
      creds: grpc.ChannelCredentials,
    ) => grpc.Client;
    const client = new Client(srv.address, grpc.credentials.createInsecure());
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const call = (client as any).Execute({
      node_id: "n-1",
      instance_id: "i-1",
      node_type: "stub-agent",
      userdata: {
        fields: {
          model: { string_value: "sonnet" },
          system_prompt: { string_value: "sys" },
          user_prompt_template: { string_value: "usr" },
        },
      },
      attributes: { fields: {} },
      attributes_schema: { fields: {} },
      stores: {},
      callback_url: fakeCallbackUrl,
      cancel_token: "cancel-tok-1",
    });

    const events: ExecuteEvent[] = [];
    await new Promise<void>((resolve, reject) => {
      call.on("data", (ev: ExecuteEvent) => events.push(ev));
      call.on("error", reject);
      call.on("end", resolve);
    });

    expect(events.length).toBeGreaterThanOrEqual(2);
    expect(events[0]!.heartbeat).toBeDefined();
    const terminal = events[events.length - 1]!;
    expect(terminal.async_accepted).toBeDefined();
    const ackId = terminal.async_accepted!.async_ack_id;
    expect(ackId).toBeTruthy();

    // Wait for the background agent run + callback POST. Stub is ~50ms.
    await waitFor(() => callbackPosts.length > 0, 2000);
    expect(callbackPosts).toHaveLength(1);
    // Executor appends /v1/callback/{ackID} to the supervisor-provided base.
    expect(callbackPosts[0]!.url).toBe(
      `${fakeCallbackUrl}/v1/callback/${encodeURIComponent(ackId)}`,
    );
    const body = callbackPosts[0]!.body as Record<string, unknown>;
    expect(body.type).toBe("complete");
    // Spec §12.2/§12.3: Complete callbacks carry `attributes_delta`
    // (the legacy `result` field has been retired).
    expect(body.attributes_delta).toEqual({ stub: true });
    expect(body.changed).toBe(true);

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (client as any).close?.();
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

// gRPC Execute observability: dashboard fetches the trace via
// dispatch_id, so the ledger must record step_started on receipt and
// step_completed on outcome, then markComplete so the SSE/snapshot
// surfaces close.
describe("gRPC Execute observability ledger", () => {
  let cb: CallbackServerHandle;
  let srv: RunningServer;
  let obs: Observability;
  const fakeCli: CliRunner = {
    spawn: async () => {
      throw new Error("should not be called in stub mode");
    },
  };
  const callbackPosts: Array<{ url: string; body: unknown }> = [];

  beforeEach(async () => {
    process.env.RIMSKY_EXECUTOR_STUB_MODE = "1";
    callbackPosts.length = 0;
    obs = new Observability();
    cb = await startInternalMcpServer({ logger });
    srv = await startGrpcServer({
      host: "127.0.0.1",
      port: 0,
      callback: cb,
      cliRunner: fakeCli,
      silenceTimeoutMs: 5000,
      logger,
      observability: obs,
      postCallback: async (url, body) => {
        callbackPosts.push({ url, body });
      },
    });
  });

  afterEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    await srv.shutdown();
    await cb.close();
  });

  it("records step_started + step_completed and markComplete keyed by dispatch_id", async () => {
    const pkg = loadNodeExecutorProto();
    const Client = pkg.rimsky.v1.NodeExecutor as unknown as new (
      addr: string,
      creds: grpc.ChannelCredentials,
    ) => grpc.Client;
    const client = new Client(srv.address, grpc.credentials.createInsecure());
    const dispatchId = "trace-d1";
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const call = (client as any).Execute({
      node_id: "n-obs",
      instance_id: "i-obs",
      node_type: "stub-agent",
      dispatch_id: dispatchId,
      userdata: {
        fields: {
          model: { string_value: "sonnet" },
          system_prompt: { string_value: "sys" },
          user_prompt_template: { string_value: "usr" },
        },
      },
      attributes: { fields: {} },
      attributes_schema: { fields: {} },
      stores: {},
      callback_url: "http://supervisor.invalid/cb",
      cancel_token: "tok",
    });
    await new Promise<void>((resolve, reject) => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      call.on("data", () => {});
      call.on("error", reject);
      call.on("end", resolve);
    });
    await waitFor(() => callbackPosts.length > 0, 2000);

    const trace = obs.getTrace(dispatchId);
    expect(trace.dispatch_id).toBe(dispatchId);
    // Successful stub run records step_started + step_completed plus
    // the synthetic trace_complete marker added by markComplete.
    const cats = trace.events.map((e) => e.category);
    expect(cats).toContain("step_started");
    expect(cats).toContain("step_completed");
    expect(cats).toContain("trace_complete");
    expect(trace.complete).toBe(true);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (client as any).close?.();
  });
});

// End-to-end coverage of the TS executor -> Go supervisor callback protocol.
// Rather than spin up a full Go supervisor here (out of scope for TS tests),
// we stand up a plain HTTP server that mimics the supervisor's chi routing:
//   POST /v1/callback/{ackID}  -> captures ackID from path + body
// This guarantees the executor's URL shape and body format line up with the
// supervisor's handler in core/supervisor/callback.go.
describe("gRPC executor -> supervisor callback (protocol shape)", () => {
  let cb: CallbackServerHandle;
  let srv: RunningServer;
  let supervisorLike: http.Server;
  let supervisorBase: string;
  const received: Array<{ path: string; ackId: string; body: any }> = [];

  const fakeCli: CliRunner = {
    spawn: async () => {
      throw new Error("should not be called in stub mode");
    },
  };

  beforeEach(async () => {
    process.env.RIMSKY_EXECUTOR_STUB_MODE = "1";
    received.length = 0;

    // Minimal supervisor-like HTTP server that matches the Go chi route
    // `POST /v1/callback/{ackID}`. This is the end-to-end assertion.
    supervisorLike = http.createServer((req, res) => {
      const match = /^\/v1\/callback\/([^/]+)$/.exec(req.url ?? "");
      if (!match || req.method !== "POST") {
        res.statusCode = 404;
        res.end(JSON.stringify({ error: "not found" }));
        return;
      }
      const ackId = decodeURIComponent(match[1]!);
      const chunks: Buffer[] = [];
      req.on("data", (c) => chunks.push(c));
      req.on("end", () => {
        let parsed: unknown;
        try {
          parsed = JSON.parse(Buffer.concat(chunks).toString("utf8"));
        } catch {
          res.statusCode = 400;
          res.end(JSON.stringify({ error: "invalid json" }));
          return;
        }
        received.push({ path: req.url ?? "", ackId, body: parsed });
        res.statusCode = 200;
        res.setHeader("content-type", "application/json");
        res.end(JSON.stringify({ status: "accepted" }));
      });
    });
    await new Promise<void>((resolve) =>
      supervisorLike.listen(0, "127.0.0.1", () => resolve()),
    );
    const addr = supervisorLike.address() as AddressInfo;
    supervisorBase = `http://127.0.0.1:${addr.port}`;

    cb = await startInternalMcpServer({ logger });
    srv = await startGrpcServer({
      host: "127.0.0.1",
      port: 0,
      callback: cb,
      cliRunner: fakeCli,
      silenceTimeoutMs: 5000,
      logger,
      // Use the real defaultPostCallback (no postCallback override) so we
      // actually exercise the network round-trip.
    });
  });

  afterEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    await srv.shutdown();
    await cb.close();
    await new Promise<void>((resolve) => supervisorLike.close(() => resolve()));
  });

  it("POSTs to /v1/callback/{ackID} with a body keyed by `type`", async () => {
    const pkg = loadNodeExecutorProto();
    const Client = pkg.rimsky.v1.NodeExecutor as unknown as new (
      addr: string,
      creds: grpc.ChannelCredentials,
    ) => grpc.Client;
    const client = new Client(srv.address, grpc.credentials.createInsecure());
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const call = (client as any).Execute({
      node_id: "n-e2e",
      instance_id: "i-e2e",
      node_type: "stub-agent",
      userdata: {
        fields: {
          model: { string_value: "sonnet" },
          system_prompt: { string_value: "sys" },
          user_prompt_template: { string_value: "usr" },
        },
      },
      attributes: { fields: {} },
      attributes_schema: { fields: {} },
      stores: {},
      callback_url: supervisorBase,
      cancel_token: "cancel-tok-e2e",
    });

    interface ExecuteEventLocal {
      heartbeat?: { timestamp_ms: number; note: string };
      async_accepted?: { async_ack_id: string; expected_completion_ms: number };
    }
    const events: ExecuteEventLocal[] = [];
    await new Promise<void>((resolve, reject) => {
      call.on("data", (ev: ExecuteEventLocal) => events.push(ev));
      call.on("error", reject);
      call.on("end", resolve);
    });
    const terminal = events[events.length - 1]!;
    const ackId = terminal.async_accepted!.async_ack_id;
    expect(ackId).toBeTruthy();

    await waitFor(() => received.length > 0, 3000);
    expect(received).toHaveLength(1);
    expect(received[0]!.path).toBe(
      `/v1/callback/${encodeURIComponent(ackId)}`,
    );
    expect(received[0]!.ackId).toBe(ackId);
    expect(received[0]!.body.type).toBe("complete");
    // Ensure we did NOT use the legacy `kind` key.
    expect(received[0]!.body.kind).toBeUndefined();
    // Spec §12.2: stub round-trips its synthetic delta.
    expect(received[0]!.body.attributes_delta).toEqual({ stub: true });

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (client as any).close?.();
  });
});
