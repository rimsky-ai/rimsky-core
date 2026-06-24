// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { randomUUID } from "node:crypto";
import * as grpc from "@grpc/grpc-js";
import type { Logger } from "pino";
import { loadExecutorProto } from "./proto-loader.js";
import { runAgent, type AgentOutcome, type HostMcpServerInput } from "./agent-run.js";
import type { CallbackServerHandle } from "./internal-mcp-server.js";
import type { CliRunner } from "./cli-runner.js";
import { createClaudeCliRunner } from "./cli-runner.js";
import type { CliAuthConfig } from "./cli-env.js";
import type { Observability, TraceEvent } from "./observability.js";
import { expectedAttributesSchemaBytes, resolveDeclaredTags, declaredErrorClasses } from "./expected-attributes-schema.js";
import { CliConfigError, isCliConfigError } from "./cli-config-error.js";
import type { McpCatalog } from "./mcp-catalog.js";
import { sessionTokenFromScratch, sessionTokenToScratchBase64 } from "./session-token-scratch.js";

export interface GrpcServerConfig {
  host: string;
  port: number;
  callback: CallbackServerHandle;
  cliRunner?: CliRunner;
  cliAuth?: CliAuthConfig;
  silenceTimeoutMsDefault: number;
  toolUseTimeoutMsDefault: number;
  logger: Logger;
  mcpCatalog?: McpCatalog;
  mcpAllowInline?: boolean;
  postCallback?: PostCallbackFn;
  observability?: Observability;
  observabilityHttpBridgeUrl?: string;
}

export type PostCallbackFn = (
  url: string,
  body: unknown,
  logger: Logger,
) => Promise<void>;

export interface RunningServer {
  address: string;
  shutdown(): Promise<void>;
}

interface ExecuteRequest {
  node_id?: string;
  instance_id?: string;
  node_type?: string;
  attributes?: unknown;
  attributes_schema?: unknown;
  claim_producers?: Record<string, unknown>;
  callback_url?: string;
  cancel_token?: string;
  dispatch_id?: string;
  prior_dispatch_id?: string;
  prior_dispatch_disposition?: string;
  run_scope_id?: string;
  scratch?: Buffer | Uint8Array;
}

interface OutcomeWire {
  await_async?: { async_ack_id: string; expected_completion_ms: number };
}

type ExecuteUnaryCall = grpc.ServerUnaryCall<ExecuteRequest, OutcomeWire>;
type ExecuteUnaryReply = grpc.sendUnaryData<OutcomeWire>;

export async function startGrpcServer(
  config: GrpcServerConfig,
): Promise<RunningServer> {
  const pkg = loadExecutorProto();
  const server = new grpc.Server();
  const post = config.postCallback ?? defaultPostCallback;
  const cliRunner = config.cliRunner ?? createClaudeCliRunner({
    auth: requireAuth(config.cliAuth),
  });

  server.addService(pkg.rimsky.v1.Executor.service, {
    Execute: (call: ExecuteUnaryCall, reply: ExecuteUnaryReply) =>
      handleExecute(call, reply, config, cliRunner, post),
  });

  server.addService(pkg.rimsky.v1.ExecutorObservability.service, {
    Capabilities: (
      _call: grpc.ServerUnaryCall<unknown, unknown>,
      cb: grpc.sendUnaryData<unknown>,
    ) => {
      cb(null, {
        supports_trace_get: true,
        supports_trace_stream: true,
        retention_after_terminal_seconds: 3600,
        custom_ui: null,
        http_bridge_url: config.observabilityHttpBridgeUrl ?? "",
        expected_attributes_schema: Buffer.from(expectedAttributesSchemaBytes()),
        declared_tags: resolveDeclaredTags(),
        declared_error_classes: declaredErrorClasses,
      });
    },
    GetTrace: (
      call: grpc.ServerUnaryCall<{ dispatch_id?: string }, unknown>,
      cb: grpc.sendUnaryData<unknown>,
    ) => {
      const dispatchId = call.request.dispatch_id ?? "";
      if (!dispatchId) {
        cb({
          code: grpc.status.INVALID_ARGUMENT,
          message: "dispatch_id required",
        });
        return;
      }
      const obs = config.observability;
      if (!obs) {
        cb(null, {
          dispatch_id: dispatchId,
          evicted: true,
          complete: true,
          events: [],
        });
        return;
      }
      const trace = obs.getTrace(dispatchId);
      cb(null, {
        dispatch_id: trace.dispatch_id,
        evicted: trace.evicted,
        complete: trace.complete,
        events: trace.events.map(traceEventToProto),
      });
    },
    StreamTrace: (
      call: grpc.ServerWritableStream<{ dispatch_id?: string }, unknown>,
    ) => {
      const dispatchId = call.request.dispatch_id ?? "";
      if (!dispatchId) {
        call.emit("error", {
          code: grpc.status.INVALID_ARGUMENT,
          message: "dispatch_id required",
        });
        return;
      }
      const obs = config.observability;
      if (!obs) {
        call.write(traceEventToProto({
          event_id: randomUUID(),
          timestamp: new Date().toISOString(),
          severity: "INFO",
          category: "trace_complete",
        }));
        call.end();
        return;
      }
      const idleMs = Number(process.env.RIMSKY_OBS_IDLE_TIMEOUT_MS ?? 5 * 60 * 1000);
      let idleTimer: ReturnType<typeof setTimeout> | null = null;
      let closed = false;
      const closeStream = (): void => {
        if (closed) return;
        closed = true;
        if (idleTimer !== null) {
          clearTimeout(idleTimer);
          idleTimer = null;
        }
        result.unsubscribe();
        call.end();
      };
      const armIdle = (): void => {
        if (closed || idleMs <= 0) return;
        if (idleTimer !== null) clearTimeout(idleTimer);
        idleTimer = setTimeout(() => {
          idleTimer = null;
          closeStream();
        }, idleMs);
      };
      const result = obs.subscribeWithSnapshot(dispatchId, (ev) => {
        if (closed) return;
        call.write(traceEventToProto(ev));
        if (ev.category === "trace_complete") {
          closeStream();
          return;
        }
        armIdle();
      });
      for (const ev of result.snapshot.events) {
        call.write(traceEventToProto(ev));
      }
      if (result.snapshot.complete || result.snapshot.evicted) {
        closeStream();
        return;
      }
      armIdle();
      call.on("cancelled", () => {
        closeStream();
      });
    },
  });

  const bindAddr = `${config.host}:${config.port}`;
  const boundPort = await new Promise<number>((resolve, reject) => {
    server.bindAsync(
      bindAddr,
      grpc.ServerCredentials.createInsecure(),
      (err, port) => {
        if (err) return reject(err);
        resolve(port);
      },
    );
  });

  const actualAddr = `${config.host}:${boundPort}`;
  config.logger.info({ addr: actualAddr }, "claude-agent gRPC server listening");

  return {
    address: actualAddr,
    shutdown: () =>
      new Promise<void>((resolve) => {
        server.tryShutdown((err) => {
          if (err) {
            config.logger.warn({ err: String(err) }, "forcing grpc shutdown");
            server.forceShutdown();
          }
          resolve();
        });
      }),
  };
}

function handleExecute(
  call: ExecuteUnaryCall,
  reply: ExecuteUnaryReply,
  config: GrpcServerConfig,
  cliRunner: CliRunner,
  post: PostCallbackFn,
): void {
  const req = call.request;
  const ackId = randomUUID();
  const runId = req.dispatch_id && req.dispatch_id.length > 0
    ? req.dispatch_id
    : randomUUID();
  const traceId = req.dispatch_id && req.dispatch_id.length > 0
    ? req.dispatch_id
    : ackId;
  const logger = config.logger.child({
    run_id: runId,
    node_id: req.node_id,
    node_type: req.node_type,
    dispatch_id: req.dispatch_id,
  });

  logger.info(
    {
      instance_id: req.instance_id,
      model: stringOrUndefined(toRecord(req.attributes).model),
      cwd_from_store: stringOrUndefined(toRecord(req.attributes).cwd_from_store),
      claim_producers: Object.keys(req.claim_producers ?? {}),
    },
    "execute.received",
  );

  if (config.observability) {
    config.observability.recordEvent(traceId, {
      category: "step_started",
      attributes: {
        step_id: "dispatch",
        node_id: req.node_id,
        node_type: req.node_type,
      },
    });
  }

  reply(null, {
    await_async: {
      async_ack_id: ackId,
      expected_completion_ms: 0,
    },
  });

  void runAndCallback(req, ackId, traceId, runId, config, cliRunner, post, logger);
}

async function runAndCallback(
  req: ExecuteRequest,
  ackId: string,
  traceId: string,
  runId: string,
  config: GrpcServerConfig,
  cliRunner: CliRunner,
  post: PostCallbackFn,
  logger: Logger,
): Promise<void> {
  await new Promise((r) => setTimeout(r, 100));
  try {
    const attributes = toRecord(req.attributes);
    const outcome = await runAgent({
      runId,
      nodeId: req.node_id ?? runId,
      nodeType: req.node_type ?? "unknown",
      model: stringOr(attributes.model, "claude-sonnet-4-5"),
      systemPrompt: stringOr(attributes.system_prompt, ""),
      userPrompt: stringOr(attributes.user_prompt, ""),
      attributesSchema: req.attributes_schema ?? {},
      attributes,
      claimProducers: unwrapClaimProducers(req.claim_producers ?? {}),
      cwdFromStore: stringOrUndefined(attributes.cwd_from_store),
      cwdOverride: stringOrUndefined(attributes.cwd),
      cliConfig: parseCliConfig(attributes.cli),
      mcpCatalog: config.mcpCatalog,
      mcpAllowInline: config.mcpAllowInline,
      dispatchId: req.dispatch_id ?? "",
      runScopeId: req.run_scope_id ?? "",
      priorDispatchId: req.prior_dispatch_id ?? "",
      priorDispatchDisposition: req.prior_dispatch_disposition ?? "",
      callbackUrl: req.callback_url ?? "",
      cancelToken: req.cancel_token ?? "",
      cliRunner,
      callback: config.callback,
      silenceTimeoutMsDefault: config.silenceTimeoutMsDefault,
      toolUseTimeoutMsDefault: config.toolUseTimeoutMsDefault,
      logger,
      sessionToken: sessionTokenFromScratch(req.scratch) ?? stringOr(attributes.session_token, ""),
    });
    const body = outcomeToCallbackBody(outcome);
    if (config.observability) {
      const attrs: Record<string, unknown> = { step_id: "dispatch" };
      let cat: string;
      switch (outcome.kind) {
        case "complete":
          cat = "step_completed";
          break;
        case "errored":
          cat = "step_failed";
          attrs.error = outcome.errorClass;
          break;
        case "blocked":
          cat = "step_blocked";
          attrs.reason = outcome.reason;
          break;
        case "park_requested":
          cat = "step_parked";
          attrs.reason = outcome.reason;
          break;
      }
      config.observability.recordEvent(traceId, { category: cat, attributes: attrs });
      config.observability.markComplete(traceId);
    }
    if (req.callback_url) {
      await post(buildCallbackUrl(req.callback_url, ackId), body, logger);
    } else {
      logger.warn({ outcome: outcome.kind }, "no callback_url; outcome dropped");
    }
  } catch (e) {
    const errorClass = isCliConfigError(e)
      ? e.errorClass
      : "agent/internal_error";
    logger.error({ error: String(e), error_class: errorClass }, "agent run failed");
    if (config.observability) {
      config.observability.recordEvent(traceId, {
        category: "error",
        severity: "ERROR",
        attributes: { error: String(e), error_class: errorClass },
      });
      config.observability.markComplete(traceId);
    }
    if (req.callback_url) {
      await post(
        buildCallbackUrl(req.callback_url, ackId),
        {
          error: {
            error_class: errorClass,
            payload: { error: String(e) },
          },
        },
        logger,
      ).catch(() => {});
    }
  }
}

function buildCallbackUrl(base: string, ackId: string): string {
  const trimmed = base.replace(/\/+$/, "");
  return `${trimmed}/v1/callback/${encodeURIComponent(ackId)}`;
}

export function outcomeToCallbackBody(
  outcome: AgentOutcome,
): Record<string, unknown> {
  if (outcome.kind === "complete") {
    return {
      success: {
        attributes_delta: outcome.attributesDelta,
        changed: outcome.changed,
        change_summary: outcome.changeSummary,
      },
    };
  }
  if (outcome.kind === "blocked") {
    return {
      error: {
        error_class: "agent/blocked",
        payload: { reason: outcome.reason, context: outcome.context },
      },
    };
  }
  if (outcome.kind === "park_requested") {
    const parkBody: Record<string, unknown> = {
      reason: outcome.reason,
      reason_note: outcome.reasonNote ?? "",
    };
    if (outcome.resumeAt) {
      parkBody.resume_at = outcome.resumeAt.toISOString();
    }
    if (outcome.sessionToken && outcome.sessionToken.length > 0) {
      parkBody.scratch = sessionTokenToScratchBase64(outcome.sessionToken);
    }
    return { park: parkBody };
  }
  return {
    error: {
      error_class: outcome.errorClass,
      payload: outcome.payload,
    },
  };
}

export function unwrapStructValue(v: unknown): unknown {
  if (v === null || v === undefined) return null;
  if (typeof v !== "object") return v;
  const o = v as Record<string, unknown>;
  const kind = typeof o.kind === "string" ? o.kind : undefined;
  if (kind === "string_value" || kind === "stringValue" || (kind === undefined && typeof (o.string_value ?? o.stringValue) === "string")) {
    const s = (o.string_value ?? o.stringValue);
    return typeof s === "string" ? s : "";
  }
  if (kind === "number_value" || kind === "numberValue" || (kind === undefined && typeof (o.number_value ?? o.numberValue) === "number")) {
    const n = (o.number_value ?? o.numberValue);
    return typeof n === "number" ? n : 0;
  }
  if (kind === "bool_value" || kind === "boolValue" || (kind === undefined && typeof (o.bool_value ?? o.boolValue) === "boolean")) {
    const b = (o.bool_value ?? o.boolValue);
    return typeof b === "boolean" ? b : false;
  }
  if (kind === "null_value" || kind === "nullValue") return null;
  if (kind === "struct_value" || kind === "structValue") {
    return unwrapStruct(o.struct_value ?? o.structValue);
  }
  if (kind === "list_value" || kind === "listValue") {
    const lv = (o.list_value ?? o.listValue) as { values?: unknown[] } | undefined;
    return (lv?.values ?? []).map(unwrapStructValue);
  }
  return v;
}

export function unwrapStruct(v: unknown): Record<string, unknown> {
  if (!v || typeof v !== "object") return {};
  const fields = (v as { fields?: Record<string, unknown> }).fields;
  if (!fields || typeof fields !== "object") return {};
  const out: Record<string, unknown> = {};
  for (const [k, val] of Object.entries(fields)) {
    out[k] = unwrapStructValue(val);
  }
  return out;
}

function toRecord(v: unknown): Record<string, unknown> {
  if (!v || typeof v !== "object" || Array.isArray(v)) return {};
  if ("fields" in v && typeof (v as { fields?: unknown }).fields === "object") {
    return unwrapStruct(v);
  }
  return v as Record<string, unknown>;
}

function unwrapClaimProducers(claimProducers: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(claimProducers)) {
    if (!v || typeof v !== "object") {
      out[k] = v;
      continue;
    }
    const sh = v as { kind?: unknown; handle?: unknown };
    out[k] = {
      kind: sh.kind,
      handle: sh.handle ? unwrapStruct(sh.handle) : {},
    };
  }
  return out;
}

function stringOr(v: unknown, fallback: string): string {
  return typeof v === "string" ? v : fallback;
}

function stringOrUndefined(v: unknown): string | undefined {
  return typeof v === "string" && v.length > 0 ? v : undefined;
}

function boolOrUndefined(v: unknown): boolean | undefined {
  return typeof v === "boolean" ? v : undefined;
}

function stringArrayOrUndefined(v: unknown): string[] | undefined {
  if (!Array.isArray(v)) return undefined;
  const out: string[] = [];
  for (const item of v) {
    if (typeof item === "string" && item.length > 0) out.push(item);
  }
  return out.length > 0 ? out : undefined;
}

export function parseCliConfig(v: unknown): {
  bare?: boolean;
  permissionMode?: string;
  allowedTools?: string[];
  disallowedTools?: string[];
  addDirs?: string[];
  maxBudgetUsd?: string;
  handleRateLimits?: boolean;
  maxSchemaCorrections?: number;
  mcpServers?: HostMcpServerInput[];
  requiredSignoffs?: { publicKey: string; path?: string }[];
  maxSignoffAttempts?: number;
  silenceTimeoutMs?: number;
  toolUseTimeoutMs?: number;
} | undefined {
  const cli = toRecord(v);
  if (Object.keys(cli).length === 0) return undefined;
  const out: ReturnType<typeof parseCliConfig> = {};
  const bare = boolOrUndefined(cli.bare);
  if (bare !== undefined) out!.bare = bare;
  const pm = stringOrUndefined(cli.permission_mode);
  if (pm !== undefined) out!.permissionMode = pm;
  const at = stringArrayOrUndefined(cli.allowed_tools);
  if (at !== undefined) out!.allowedTools = at;
  const dt = stringArrayOrUndefined(cli.disallowed_tools);
  if (dt !== undefined) out!.disallowedTools = dt;
  const ad = stringArrayOrUndefined(cli.add_dirs);
  if (ad !== undefined) out!.addDirs = ad;
  const mb = stringOrUndefined(cli.max_budget_usd);
  if (mb !== undefined) out!.maxBudgetUsd = mb;
  const hr = boolOrUndefined(cli.handle_rate_limits);
  if (hr !== undefined) out!.handleRateLimits = hr;
  const msc = numberOrUndefined(cli.max_schema_corrections);
  if (msc !== undefined) out!.maxSchemaCorrections = msc;
  const ms = parseMcpServers(cli.mcp_servers);
  if (ms !== undefined) out!.mcpServers = ms;
  const rs = parseRequiredSignoffs(cli.required_signoffs);
  if (rs !== undefined) out!.requiredSignoffs = rs;
  const msa = numberOrUndefined(cli.max_signoff_attempts);
  if (msa !== undefined) out!.maxSignoffAttempts = msa;
  const stm = nonNegativeIntOrUndefined(cli.silence_timeout_ms, "cli.silence_timeout_ms");
  if (stm !== undefined) out!.silenceTimeoutMs = stm;
  const tutm = nonNegativeIntOrUndefined(cli.tool_use_timeout_ms, "cli.tool_use_timeout_ms");
  if (tutm !== undefined) out!.toolUseTimeoutMs = tutm;
  return Object.keys(out!).length > 0 ? out : undefined;
}

function nonNegativeIntOrUndefined(v: unknown, field: string): number | undefined {
  if (v === undefined || v === null) return undefined;
  if (typeof v !== "number" || !Number.isFinite(v) || v < 0 || !Number.isInteger(v)) {
    throw new CliConfigError(
      `${field} must be a non-negative integer (ms), got ${typeof v === "number" ? v : typeof v}`,
    );
  }
  return v;
}

function parseMcpServers(v: unknown): HostMcpServerInput[] | undefined {
  if (v === undefined || v === null) return undefined;
  if (!Array.isArray(v)) {
    throw new CliConfigError(
      `cli.mcp_servers must be an array, got ${typeof v}`,
    );
  }
  const out: HostMcpServerInput[] = [];
  for (const [i, item] of v.entries()) {
    if (!item || typeof item !== "object" || Array.isArray(item)) {
      throw new CliConfigError(`cli.mcp_servers[${i}] must be an object`);
    }
    const e = item as Record<string, unknown>;
    if ("ref" in e) {
      if (typeof e.ref !== "string" || e.ref.length === 0) {
        throw new CliConfigError(
          `cli.mcp_servers[${i}].ref must be a non-empty string`,
        );
      }
      out.push({ ref: e.ref });
      continue;
    }
    if (typeof e.name !== "string" || e.name.length === 0) {
      throw new CliConfigError(
        `cli.mcp_servers[${i}].name must be a non-empty string`,
      );
    }
    if (typeof e.url !== "string" || e.url.length === 0) {
      throw new CliConfigError(
        `cli.mcp_servers[${i}].url must be a non-empty string`,
      );
    }
    const entry: {
      name: string;
      url: string;
      headers?: Record<string, string>;
      allowedTools?: string[];
    } = { name: e.name, url: e.url };
    const headers = parseStringRecord(e.headers);
    if (headers !== undefined) entry.headers = headers;
    const allowedTools = stringArrayOrUndefined(e.allowed_tools);
    if (allowedTools !== undefined) entry.allowedTools = allowedTools;
    out.push(entry);
  }
  return out.length > 0 ? out : undefined;
}

function parseRequiredSignoffs(
  v: unknown,
): { publicKey: string; path?: string }[] | undefined {
  if (v === undefined || v === null) return undefined;
  if (!Array.isArray(v)) {
    throw new CliConfigError(
      `cli.required_signoffs must be an array, got ${typeof v}`,
    );
  }
  const out: { publicKey: string; path?: string }[] = [];
  for (const [i, item] of v.entries()) {
    if (!item || typeof item !== "object" || Array.isArray(item)) {
      throw new CliConfigError(`cli.required_signoffs[${i}] must be an object`);
    }
    const e = item as Record<string, unknown>;
    if (typeof e.public_key !== "string" || e.public_key.length === 0) {
      throw new CliConfigError(
        `cli.required_signoffs[${i}].public_key must be a non-empty string`,
      );
    }
    const entry: { publicKey: string; path?: string } = { publicKey: e.public_key };
    if (typeof e.path === "string" && e.path.length > 0) entry.path = e.path;
    out.push(entry);
  }
  return out.length > 0 ? out : undefined;
}

function parseStringRecord(v: unknown): Record<string, string> | undefined {
  if (!v || typeof v !== "object" || Array.isArray(v)) return undefined;
  const out: Record<string, string> = {};
  for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
    if (typeof val === "string") out[k] = val;
  }
  return Object.keys(out).length > 0 ? out : undefined;
}

function numberOrUndefined(v: unknown): number | undefined {
  return typeof v === "number" && Number.isFinite(v) ? v : undefined;
}

function requireAuth(auth: CliAuthConfig | undefined): CliAuthConfig {
  if (!auth) {
    throw new Error(
      "claude-agent: cliAuth is required when no cliRunner is supplied — pass auth from main.ts",
    );
  }
  return auth;
}

export const defaultPostCallback: PostCallbackFn = async (url, body, logger) => {
  try {
    const res = await fetch(url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      logger.warn(
        { status: res.status, url },
        "callback POST returned non-2xx",
      );
    }
  } catch (e) {
    logger.error({ error: String(e), url }, "callback POST failed");
  }
};

export function traceEventToProto(ev: TraceEvent): Record<string, unknown> {
  return {
    event_id: ev.event_id,
    parent_event_id: ev.parent_event_id ?? "",
    timestamp: isoToProtoTimestamp(ev.timestamp),
    severity: ev.severity ?? "SEVERITY_UNSPECIFIED",
    category: ev.category,
    message: ev.message ?? "",
    attributes: jsToProtoStruct(ev.attributes),
  };
}

export function isoToProtoTimestamp(iso: string): { seconds: string; nanos: number } {
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return { seconds: "0", nanos: 0 };
  const seconds = Math.floor(ms / 1000);
  const nanos = (ms - seconds * 1000) * 1_000_000;
  return { seconds: seconds.toString(), nanos };
}

export function jsToProtoStruct(value: unknown): { fields: Record<string, unknown> } | null {
  if (value === null || value === undefined) return null;
  if (typeof value !== "object" || Array.isArray(value)) return null;
  const fields: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
    fields[k] = jsToProtoValue(v);
  }
  return { fields };
}

export function jsToProtoValue(value: unknown): Record<string, unknown> {
  if (value === null || value === undefined) {
    return { null_value: "NULL_VALUE" };
  }
  if (typeof value === "string") return { string_value: value };
  if (typeof value === "number") return { number_value: value };
  if (typeof value === "boolean") return { bool_value: value };
  if (Array.isArray(value)) {
    return { list_value: { values: value.map(jsToProtoValue) } };
  }
  if (typeof value === "object") {
    return { struct_value: jsToProtoStruct(value) ?? { fields: {} } };
  }
  return { string_value: String(value) };
}
