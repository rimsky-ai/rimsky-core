// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect, beforeEach, afterEach } from "vitest";
import * as grpc from "@grpc/grpc-js";
import pino from "pino";
import * as http from "node:http";
import type { AddressInfo } from "node:net";
import { loadExecutorProto } from "./proto-loader.js";
import {
  startGrpcServer,
  type RunningServer,
  isoToProtoTimestamp,
  jsToProtoStruct,
  jsToProtoValue,
  outcomeToCallbackBody,
  traceEventToProto,
  unwrapStruct,
  unwrapStructValue,
} from "./server.js";
import type { AgentOutcome } from "./agent-run.js";
import {
  startInternalMcpServer,
  type CallbackServerHandle,
} from "./internal-mcp-server.js";
import type { CliRunner } from "./cli-runner.js";
import { Observability } from "./observability.js";

const logger = pino({ level: "silent" });

interface ExecuteEvent {
  heartbeat?: { timestamp_ms: number; note: string };
  stream_close?: {
    await_async?: { async_ack_id: string; expected_completion_ms: number };
    success?: { attributes_delta: unknown; changed: boolean; change_summary: string };
    error?: { error_class: string; payload: unknown };
    park?: { reason: string; payload: unknown; resume_at?: string; session_token?: string };
  };
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

  it("emits Heartbeat + AwaitAsyncCallback, then POSTs Success outcome with attributes_delta", async () => {
    const pkg = loadExecutorProto();
    const Client = pkg.rimsky.v1.Executor as unknown as new (
      addr: string,
      creds: grpc.ChannelCredentials,
    ) => grpc.Client;
    const client = new Client(srv.address, grpc.credentials.createInsecure());
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const call = (client as any).Execute({
      node_id: "n-1",
      instance_id: "i-1",
      node_type: "stub-agent",
      attributes: {
        fields: {
          model: { string_value: "sonnet" },
          system_prompt: { string_value: "sys" },
          user_prompt: { string_value: "usr" },
        },
      },
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
    // Post-spec:2026-05-12 (Group E.4): the stream-close event uses
    // StreamClose + outcome oneof; AwaitAsyncCallback replaces AsyncAccepted.
    expect(terminal.stream_close).toBeDefined();
    expect(terminal.stream_close!.await_async).toBeDefined();
    const ackId = terminal.stream_close!.await_async!.async_ack_id;
    expect(ackId).toBeTruthy();

    // Wait for the background agent run + callback POST. Stub is ~50ms.
    await waitFor(() => callbackPosts.length > 0, 2000);
    expect(callbackPosts).toHaveLength(1);
    // Executor appends /v1/callback/{ackID} to the supervisor-provided base.
    expect(callbackPosts[0]!.url).toBe(
      `${fakeCallbackUrl}/v1/callback/${encodeURIComponent(ackId)}`,
    );
    const body = callbackPosts[0]!.body as Record<string, unknown>;
    // Post-2026-05-12 (spec E.2/E.6): the callback body uses the
    // AsyncCallbackBody outcome-oneof shape — `success: { ... }` —
    // rather than the legacy `{type: "complete", ...}` discriminator.
    expect(body.success).toBeDefined();
    const success = body.success as Record<string, unknown>;
    expect(success.attributes_delta).toEqual({ stub: true });
    expect(success.changed).toBe(true);

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
    const pkg = loadExecutorProto();
    const Client = pkg.rimsky.v1.Executor as unknown as new (
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
      attributes: {
        fields: {
          model: { string_value: "sonnet" },
          system_prompt: { string_value: "sys" },
          user_prompt: { string_value: "usr" },
        },
      },
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
  const received: Array<{
    path: string;
    ackId: string;
    body: Record<string, unknown>;
  }> = [];

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
        received.push({
          path: req.url ?? "",
          ackId,
          body: parsed as Record<string, unknown>,
        });
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
    const pkg = loadExecutorProto();
    const Client = pkg.rimsky.v1.Executor as unknown as new (
      addr: string,
      creds: grpc.ChannelCredentials,
    ) => grpc.Client;
    const client = new Client(srv.address, grpc.credentials.createInsecure());
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const call = (client as any).Execute({
      node_id: "n-e2e",
      instance_id: "i-e2e",
      node_type: "stub-agent",
      attributes: {
        fields: {
          model: { string_value: "sonnet" },
          system_prompt: { string_value: "sys" },
          user_prompt: { string_value: "usr" },
        },
      },
      attributes_schema: { fields: {} },
      stores: {},
      callback_url: supervisorBase,
      cancel_token: "cancel-tok-e2e",
    });

    interface ExecuteEventLocal {
      heartbeat?: { timestamp_ms: number; note: string };
      stream_close?: {
        await_async?: { async_ack_id: string; expected_completion_ms: number };
      };
    }
    const events: ExecuteEventLocal[] = [];
    await new Promise<void>((resolve, reject) => {
      call.on("data", (ev: ExecuteEventLocal) => events.push(ev));
      call.on("error", reject);
      call.on("end", resolve);
    });
    const terminal = events[events.length - 1]!;
    const ackId = terminal.stream_close!.await_async!.async_ack_id;
    expect(ackId).toBeTruthy();

    await waitFor(() => received.length > 0, 3000);
    expect(received).toHaveLength(1);
    expect(received[0]!.path).toBe(
      `/v1/callback/${encodeURIComponent(ackId)}`,
    );
    expect(received[0]!.ackId).toBe(ackId);
    // Post-2026-05-12 (spec E.2/E.6): AsyncCallbackBody outcome-oneof
    // shape — `success: { ... }` — rather than the legacy
    // `{type: "complete", ...}` discriminator.
    expect(received[0]!.body.success).toBeDefined();
    // Ensure we did NOT use the legacy `kind` or `type` keys.
    expect(received[0]!.body.kind).toBeUndefined();
    expect(received[0]!.body.type).toBeUndefined();
    // Spec §12.2: stub round-trips its synthetic delta.
    const success = received[0]!.body.success as Record<string, unknown>;
    expect(success.attributes_delta).toEqual({ stub: true });

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (client as any).close?.();
  });
});

// The malformed-gate-config fail-loud guarantee, proven on the gRPC transport
// (the HTTP bridge proves the same in http-bridge.test.ts). A present-but-
// malformed cli.required_signoffs (host omitted public_key) must terminal-
// ERROR with agent/attribute_invalid, never silently degrade to an ungated
// run. parseCliConfig throws a CliConfigError in the background runAndCallback
// (server.ts ~415, BEFORE runAgent), so the stream still closes with the normal
// async handoff and the verdict rides the callback. The nested cli Struct is
// built with protobuf.js's canonical camelCase Value wrappers (structValue/
// listValue/stringValue): @grpc/proto-loader serializes Value via those names
// regardless of keepCase (which governs only decode), and the snake_case form
// jsToProtoStruct emits silently drops a nested Struct on the client side.
describe("gRPC server Execute rejects a malformed sign-off gate config (no silent ungating)", () => {
  let cb: CallbackServerHandle;
  let srv: RunningServer;
  const fakeCli: CliRunner = {
    spawn: async () => {
      throw new Error("spawn must not be reached when cli config is malformed");
    },
  };
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

  it("POSTs a terminal agent/attribute_invalid error (not success) when a required_signoffs entry omits public_key", async () => {
    const pkg = loadExecutorProto();
    const Client = pkg.rimsky.v1.Executor as unknown as new (
      addr: string,
      creds: grpc.ChannelCredentials,
    ) => grpc.Client;
    const client = new Client(srv.address, grpc.credentials.createInsecure());
    // Build the attributes Struct with protobuf.js's canonical camelCase Value
    // wrappers (structValue/listValue/stringValue) — the form @grpc/proto-loader
    // actually serializes nested Structs from on the client (see describe note).
    const toValue = (v: unknown): unknown => {
      if (v === null || v === undefined) return { nullValue: "NULL_VALUE" };
      if (typeof v === "string") return { stringValue: v };
      if (typeof v === "number") return { numberValue: v };
      if (typeof v === "boolean") return { boolValue: v };
      if (Array.isArray(v)) return { listValue: { values: v.map(toValue) } };
      const nested: Record<string, unknown> = {};
      for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
        nested[k] = toValue(val);
      }
      return { structValue: { fields: nested } };
    };
    const toStruct = (o: Record<string, unknown>) => {
      const fields: Record<string, unknown> = {};
      for (const [k, val] of Object.entries(o)) fields[k] = toValue(val);
      return { fields };
    };
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const call = (client as any).Execute({
      node_id: "n-gate",
      node_type: "stub-agent",
      dispatch_id: "d-grpc-gate",
      // public_key omitted — only `path` present. The parser must reject this
      // present-but-malformed gate rather than drop it to an ungated run.
      attributes: toStruct({
        model: "sonnet",
        user_prompt: "go",
        cli: { required_signoffs: [{ path: "endpoints" }] },
      }),
      attributes_schema: { fields: {} },
      callback_url: fakeCallbackUrl,
    });

    const events: ExecuteEvent[] = [];
    await new Promise<void>((resolve, reject) => {
      call.on("data", (ev: ExecuteEvent) => events.push(ev));
      call.on("error", reject);
      call.on("end", resolve);
    });

    // The stream completes the async handoff; the verdict rides the callback.
    const terminal = events[events.length - 1]!;
    expect(terminal.stream_close?.await_async).toBeDefined();
    const ackId = terminal.stream_close!.await_async!.async_ack_id;

    await waitFor(() => callbackPosts.length > 0, 2000);
    expect(callbackPosts).toHaveLength(1);
    expect(callbackPosts[0]!.url).toBe(
      `${fakeCallbackUrl}/v1/callback/${encodeURIComponent(ackId)}`,
    );
    const body = callbackPosts[0]!.body as Record<string, unknown>;
    // Must NOT have reached terminal success.
    expect(body.success).toBeUndefined();
    expect(body.error).toBeDefined();
    const error = body.error as { error_class: string };
    expect(error.error_class).toBe("agent/attribute_invalid");

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (client as any).close?.();
  });
});

// Round-trip the production gRPC wire shape through unwrapStruct.
// proto-loader runs with `keepCase: true` + `oneofs: true` (see
// proto-loader.ts) which produces `{kind: "string_value", string_value: "x"}`
// per Value. The dispatch path reads `attributes.model` from this shape;
// the fix in unwrapStructValue accepts both the kind-set production form
// and the kind-omitted older fixture form. Both must be covered.
describe("unwrapStruct production wire shape (kind-set discriminator)", () => {
  it("unwraps a kind-set string_value", () => {
    const wire = {
      fields: { model: { kind: "string_value", string_value: "sonnet" } },
    };
    expect(unwrapStruct(wire)).toEqual({ model: "sonnet" });
  });
  it("unwraps a kind-set number_value", () => {
    const wire = {
      fields: { delay_ms: { kind: "number_value", number_value: 250 } },
    };
    expect(unwrapStruct(wire)).toEqual({ delay_ms: 250 });
  });
  it("unwraps a kind-set bool_value", () => {
    const wire = {
      fields: { stub_probe: { kind: "bool_value", bool_value: true } },
    };
    expect(unwrapStruct(wire)).toEqual({ stub_probe: true });
  });
  it("unwraps a kind-set null_value", () => {
    const wire = { fields: { x: { kind: "null_value", null_value: 0 } } };
    expect(unwrapStruct(wire)).toEqual({ x: null });
  });
  it("unwraps a kind-set struct_value (nested object)", () => {
    const wire = {
      fields: {
        cli: {
          kind: "struct_value",
          struct_value: {
            fields: {
              bare: { kind: "bool_value", bool_value: false },
            },
          },
        },
      },
    };
    expect(unwrapStruct(wire)).toEqual({ cli: { bare: false } });
  });
  it("unwraps a kind-set list_value (heterogeneous array)", () => {
    const wire = {
      fields: {
        xs: {
          kind: "list_value",
          list_value: {
            values: [
              { kind: "string_value", string_value: "a" },
              { kind: "number_value", number_value: 1 },
              { kind: "bool_value", bool_value: false },
            ],
          },
        },
      },
    };
    expect(unwrapStruct(wire)).toEqual({ xs: ["a", 1, false] });
  });
  it("preserves the kind-omitted fixture form (legacy test shape)", () => {
    const wire = { fields: { model: { string_value: "haiku" } } };
    expect(unwrapStruct(wire)).toEqual({ model: "haiku" });
  });
  it("preserves the camelCase kind form (keepCase: false)", () => {
    const wire = {
      fields: { model: { kind: "stringValue", stringValue: "opus" } },
    };
    expect(unwrapStruct(wire)).toEqual({ model: "opus" });
  });
});

// jsToProtoValue / jsToProtoStruct / isoToProtoTimestamp / traceEventToProto
// are reached on every GetTrace + StreamTrace reply. Bugs in these silently
// corrupt traces with no visible RPC error.
describe("proto-conversion helpers", () => {
  it("jsToProtoValue: scalars + null", () => {
    expect(jsToProtoValue("x")).toEqual({ string_value: "x" });
    expect(jsToProtoValue(7)).toEqual({ number_value: 7 });
    expect(jsToProtoValue(true)).toEqual({ bool_value: true });
    expect(jsToProtoValue(null)).toEqual({ null_value: "NULL_VALUE" });
    expect(jsToProtoValue(undefined)).toEqual({ null_value: "NULL_VALUE" });
  });
  it("jsToProtoValue: arrays wrap in list_value", () => {
    expect(jsToProtoValue(["a", 1, true])).toEqual({
      list_value: {
        values: [
          { string_value: "a" },
          { number_value: 1 },
          { bool_value: true },
        ],
      },
    });
  });
  it("jsToProtoValue: nested struct wraps in struct_value", () => {
    expect(jsToProtoValue({ k: "v" })).toEqual({
      struct_value: { fields: { k: { string_value: "v" } } },
    });
  });
  it("jsToProtoStruct: null/undefined/non-object → null", () => {
    expect(jsToProtoStruct(null)).toBeNull();
    expect(jsToProtoStruct(undefined)).toBeNull();
    expect(jsToProtoStruct("nope")).toBeNull();
    expect(jsToProtoStruct([1, 2])).toBeNull();
  });
  it("jsToProtoStruct: empty object → empty fields", () => {
    expect(jsToProtoStruct({})).toEqual({ fields: {} });
  });
  it("isoToProtoTimestamp: post-epoch positive ISO", () => {
    // Date.UTC(2026, 4, 9, 12, 0, 0) — month is 0-indexed.
    const iso = "2026-05-09T12:00:00.000Z";
    const ts = isoToProtoTimestamp(iso);
    expect(ts.nanos).toBe(0);
    expect(ts.seconds).toBe(String(Date.UTC(2026, 4, 9, 12, 0, 0) / 1000));
  });
  it("isoToProtoTimestamp: fractional milliseconds → positive nanos in [0, 1e9)", () => {
    const ts = isoToProtoTimestamp("2026-05-09T12:00:00.250Z");
    expect(ts.nanos).toBe(250_000_000);
    expect(Number(ts.seconds)).toBe(Math.floor(Date.UTC(2026, 4, 9, 12, 0, 0, 250) / 1000));
  });
  it("isoToProtoTimestamp: pre-epoch sub-second uses floor (no negative nanos)", () => {
    // 1969-12-31T23:59:59.500Z = -500ms wall time. Math.trunc would
    // produce nanos=-500_000_000 which violates the proto contract;
    // Math.floor produces seconds=-1, nanos=500_000_000.
    const ts = isoToProtoTimestamp("1969-12-31T23:59:59.500Z");
    expect(ts.nanos).toBe(500_000_000);
    expect(ts.nanos).toBeGreaterThanOrEqual(0);
    expect(ts.nanos).toBeLessThan(1_000_000_000);
    expect(ts.seconds).toBe("-1");
  });
  it("isoToProtoTimestamp: NaN fallback", () => {
    expect(isoToProtoTimestamp("not-a-date")).toEqual({ seconds: "0", nanos: 0 });
  });
  it("traceEventToProto: full TraceEvent → proto shape with severity + nested attributes", () => {
    const out = traceEventToProto({
      event_id: "ev-1",
      parent_event_id: "ev-0",
      timestamp: "2026-05-09T12:00:00.000Z",
      severity: "WARN",
      category: "tool_call",
      message: "called",
      attributes: { tool: "shell", ok: true, count: 3, items: ["a"] },
    });
    expect(out.event_id).toBe("ev-1");
    expect(out.parent_event_id).toBe("ev-0");
    expect(out.severity).toBe("WARN");
    expect(out.category).toBe("tool_call");
    expect(out.message).toBe("called");
    const ts = out.timestamp as { seconds: string; nanos: number };
    expect(ts.nanos).toBe(0);
    const attrs = out.attributes as { fields: Record<string, unknown> };
    expect(attrs.fields.tool).toEqual({ string_value: "shell" });
    expect(attrs.fields.ok).toEqual({ bool_value: true });
    expect(attrs.fields.count).toEqual({ number_value: 3 });
    expect(attrs.fields.items).toEqual({
      list_value: { values: [{ string_value: "a" }] },
    });
  });
  it("traceEventToProto: optional fields default sensibly", () => {
    const out = traceEventToProto({
      event_id: "ev-2",
      timestamp: "2026-05-09T12:00:00.000Z",
      severity: "INFO",
      category: "trace_complete",
    });
    expect(out.parent_event_id).toBe("");
    expect(out.message).toBe("");
    expect(out.attributes).toBeNull();
  });
});

// unwrapStructValue scalar fallback shapes (kind absent, value field absent
// → returns sensible default). Pins the kind-omitted-fixture branch separate
// from the production-wire tests above.
describe("unwrapStructValue defensive defaults", () => {
  it("returns null for null/undefined", () => {
    expect(unwrapStructValue(null)).toBeNull();
    expect(unwrapStructValue(undefined)).toBeNull();
  });
  it("passes scalars through unchanged", () => {
    expect(unwrapStructValue("x")).toBe("x");
    expect(unwrapStructValue(7)).toBe(7);
    expect(unwrapStructValue(true)).toBe(true);
  });
});

// The gRPC ExecutorObservability.Capabilities surface must carry the
// RIMSKY_EXECUTOR_DECLARED_EVENTS-resolved list in declared_events (field 7).
// This pairs with the HTTP capabilitiesPayload coverage in observability.test.ts
// to cover "both capability surfaces."
describe("ExecutorObservability.Capabilities declared_events (gRPC surface)", () => {
  let cb: CallbackServerHandle;
  let srv: RunningServer;
  const fakeCli: CliRunner = {
    spawn: async () => {
      throw new Error("should not be called");
    },
  };

  beforeEach(async () => {
    process.env.RIMSKY_EXECUTOR_DECLARED_EVENTS = "a,b";
    cb = await startInternalMcpServer({ logger });
    srv = await startGrpcServer({
      host: "127.0.0.1",
      port: 0,
      callback: cb,
      cliRunner: fakeCli,
      silenceTimeoutMs: 5000,
      logger,
    });
  });

  afterEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_DECLARED_EVENTS;
    await srv.shutdown();
    await cb.close();
  });

  it("advertises the env-resolved declared_events over gRPC", async () => {
    const pkg = loadExecutorProto();
    const ObsClient = pkg.rimsky.v1.ExecutorObservability as unknown as new (
      addr: string,
      creds: grpc.ChannelCredentials,
    ) => grpc.Client;
    const client = new ObsClient(srv.address, grpc.credentials.createInsecure());
    const caps = await new Promise<Record<string, unknown>>((resolve, reject) => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      (client as any).Capabilities({}, (err: unknown, resp: Record<string, unknown>) => {
        if (err) return reject(err);
        resolve(resp);
      });
    });
    expect(caps.declared_events).toEqual(["a", "b"]);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (client as any).close?.();
  });
});

// outcomeToCallbackBody must ride the per-dispatch named-event buffer on the
// AsyncCallbackBody `events[]` array (the gRPC stream already closed at
// dispatch). Payloads are base64-encoded per the proto-JSON `bytes` rule the
// Go supervisor expects.
describe("outcomeToCallbackBody named-event surfacing", () => {
  it("populates events[] from the buffer with base64 payloads", () => {
    const payloadA = Buffer.from(JSON.stringify({ pct: 50 }), "utf8");
    const payloadB = Buffer.from("null", "utf8");
    const outcome: AgentOutcome = {
      kind: "complete",
      attributesDelta: { ok: true },
      changed: true,
      changeSummary: "did",
      emittedEvents: [
        { name: "progress", payload: payloadA },
        { name: "ping", payload: payloadB },
      ],
    };
    const body = outcomeToCallbackBody(outcome);
    expect(body.events).toEqual([
      { name: "progress", payload: payloadA.toString("base64") },
      { name: "ping", payload: payloadB.toString("base64") },
    ]);
    // The outcome verdict is still present alongside the events.
    expect(body.success).toBeDefined();
  });

  it("omits events[] entirely when the buffer is empty (no behavior change)", () => {
    const outcome: AgentOutcome = {
      kind: "complete",
      attributesDelta: null,
      changed: false,
      changeSummary: null,
    };
    const body = outcomeToCallbackBody(outcome);
    expect("events" in body).toBe(false);
    expect(body.success).toBeDefined();
  });

  it("rides events[] alongside an error verdict too", () => {
    const payload = Buffer.from(JSON.stringify({ detail: "x" }), "utf8");
    const outcome: AgentOutcome = {
      kind: "errored",
      errorClass: "agent/blocked",
      payload: { reason: "stuck" },
      emittedEvents: [{ name: "diag", payload }],
    };
    const body = outcomeToCallbackBody(outcome);
    expect(body.events).toEqual([
      { name: "diag", payload: payload.toString("base64") },
    ]);
    expect(body.error).toBeDefined();
  });
});
