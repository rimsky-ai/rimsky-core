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

interface UnaryOutcome {
  await_async?: { async_ack_id: string; expected_completion_ms: number };
}

function executeUnary(
  client: grpc.Client,
  req: Record<string, unknown>,
): Promise<UnaryOutcome> {
  return new Promise((resolve, reject) => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (client as any).Execute(req, (err: unknown, outcome: UnaryOutcome) => {
      if (err) reject(err);
      else resolve(outcome);
    });
  });
}

describe("gRPC server stub-mode Execute end-to-end", () => {
  let cb: CallbackServerHandle;
  let srv: RunningServer;
  const fakeCli: CliRunner = {
    spawn: async () => {
      throw new Error("should not be called in stub mode");
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

  it("returns AwaitAsyncCallback on the unary Execute reply, then POSTs Success outcome with attributes_delta", async () => {
    const pkg = loadExecutorProto();
    const Client = pkg.rimsky.v1.Executor as unknown as new (
      addr: string,
      creds: grpc.ChannelCredentials,
    ) => grpc.Client;
    const client = new Client(srv.address, grpc.credentials.createInsecure());
    const outcome = await executeUnary(client, {
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
      claim_producers: {},
      callback_url: fakeCallbackUrl,
      cancel_token: "cancel-tok-1",
    });

    expect(outcome.await_async).toBeDefined();
    const ackId = outcome.await_async!.async_ack_id;
    expect(ackId).toBeTruthy();

    await waitFor(() => callbackPosts.length > 0, 2000);
    expect(callbackPosts).toHaveLength(1);
    expect(callbackPosts[0]!.url).toBe(
      `${fakeCallbackUrl}/v1/callback/${encodeURIComponent(ackId)}`,
    );
    const body = callbackPosts[0]!.body as Record<string, unknown>;
    expect(body.success).toBeDefined();
    const success = body.success as Record<string, unknown>;
    const delta1 = success.attributes_delta as Record<string, unknown>;
    expect(delta1.stub).toBe(true);
    expect(typeof delta1.session_token).toBe("string");
    expect((delta1.session_token as string).length).toBeGreaterThan(0);
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
    await executeUnary(client, {
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
      claim_producers: {},
      callback_url: "http://supervisor.invalid/cb",
      cancel_token: "tok",
    });
    await waitFor(() => callbackPosts.length > 0, 2000);

    const trace = obs.getTrace(dispatchId);
    expect(trace.dispatch_id).toBe(dispatchId);
    const cats = trace.events.map((e) => e.category);
    expect(cats).toContain("step_started");
    expect(cats).toContain("step_completed");
    expect(cats).toContain("trace_complete");
    expect(trace.complete).toBe(true);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (client as any).close?.();
  });
});

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
    });
  });

  afterEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    await srv.shutdown();
    await cb.close();
    await new Promise<void>((resolve) => supervisorLike.close(() => resolve()));
  });

  it("POSTs to /v1/callback/{ackID} with the AsyncCallbackBody outcome-oneof shape", async () => {
    const pkg = loadExecutorProto();
    const Client = pkg.rimsky.v1.Executor as unknown as new (
      addr: string,
      creds: grpc.ChannelCredentials,
    ) => grpc.Client;
    const client = new Client(srv.address, grpc.credentials.createInsecure());
    const outcome = await executeUnary(client, {
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
      claim_producers: {},
      callback_url: supervisorBase,
      cancel_token: "cancel-tok-e2e",
    });

    expect(outcome.await_async).toBeDefined();
    const ackId = outcome.await_async!.async_ack_id;
    expect(ackId).toBeTruthy();

    await waitFor(() => received.length > 0, 3000);
    expect(received).toHaveLength(1);
    expect(received[0]!.path).toBe(
      `/v1/callback/${encodeURIComponent(ackId)}`,
    );
    expect(received[0]!.ackId).toBe(ackId);
    expect(received[0]!.body.success).toBeDefined();
    expect(received[0]!.body.kind).toBeUndefined();
    expect(received[0]!.body.type).toBeUndefined();
    const success = received[0]!.body.success as Record<string, unknown>;
    const delta2 = success.attributes_delta as Record<string, unknown>;
    expect(delta2.stub).toBe(true);
    expect(typeof delta2.session_token).toBe("string");
    expect((delta2.session_token as string).length).toBeGreaterThan(0);

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (client as any).close?.();
  });
});

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
    const outcome = await executeUnary(client, {
      node_id: "n-gate",
      node_type: "stub-agent",
      dispatch_id: "d-grpc-gate",
      attributes: toStruct({
        model: "sonnet",
        user_prompt: "go",
        cli: { required_signoffs: [{ path: "endpoints" }] },
      }),
      attributes_schema: { fields: {} },
      callback_url: fakeCallbackUrl,
    });

    expect(outcome.await_async).toBeDefined();
    const ackId = outcome.await_async!.async_ack_id;

    await waitFor(() => callbackPosts.length > 0, 2000);
    expect(callbackPosts).toHaveLength(1);
    expect(callbackPosts[0]!.url).toBe(
      `${fakeCallbackUrl}/v1/callback/${encodeURIComponent(ackId)}`,
    );
    const body = callbackPosts[0]!.body as Record<string, unknown>;
    expect(body.success).toBeUndefined();
    expect(body.error).toBeDefined();
    const error = body.error as { error_class: string };
    expect(error.error_class).toBe("agent/attribute_invalid");

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (client as any).close?.();
  });
});

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

describe("ExecutorObservability.Capabilities declared_tags (gRPC surface)", () => {
  let cb: CallbackServerHandle;
  let srv: RunningServer;
  const fakeCli: CliRunner = {
    spawn: async () => {
      throw new Error("should not be called");
    },
  };

  beforeEach(async () => {
    process.env.RIMSKY_EXECUTOR_DECLARED_TAGS = "a,b";
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
    delete process.env.RIMSKY_EXECUTOR_DECLARED_TAGS;
    await srv.shutdown();
    await cb.close();
  });

  it("advertises the env-resolved declared_tags over gRPC", async () => {
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
    expect(caps.declared_tags).toEqual(["a", "b"]);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (client as any).close?.();
  });
});

describe("outcomeToCallbackBody park outcome shape", () => {
  it("encodes sessionToken into scratch on Park (Park does not carry attributes_delta)", () => {
    const outcome: AgentOutcome = {
      kind: "park_requested",
      reason: "snooze",
      reasonNote: "rate limited",
      attributesDelta: null,
      resumeAt: new Date("2026-06-17T12:00:00Z"),
      sessionToken: "sess-abc",
    };
    const body = outcomeToCallbackBody(outcome);
    expect(body.park).toBeDefined();
    const park = body.park as Record<string, unknown>;
    expect(park.reason).toBe("snooze");
    expect(park.reason_note).toBe("rate limited");
    expect(park.resume_at).toBe("2026-06-17T12:00:00.000Z");
    expect(park.session_token).toBeUndefined();
    expect(park.payload).toBeUndefined();
    expect(park.attributes_delta).toBeUndefined();
    expect(typeof park.scratch).toBe("string");
    expect(Buffer.from(park.scratch as string, "base64").toString("utf8")).toBe("sess-abc");
  });

  it("omits resume_at when null and scratch when sessionToken is empty", () => {
    const outcome: AgentOutcome = {
      kind: "park_requested",
      reason: "await_callback",
      reasonNote: "",
      attributesDelta: null,
      resumeAt: null,
      sessionToken: "",
    };
    const body = outcomeToCallbackBody(outcome);
    const park = body.park as Record<string, unknown>;
    expect("resume_at" in park).toBe(false);
    expect("attributes_delta" in park).toBe(false);
    expect("payload" in park).toBe(false);
    expect("session_token" in park).toBe(false);
    expect("scratch" in park).toBe(false);
  });
});
