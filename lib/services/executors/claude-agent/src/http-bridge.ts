// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { randomUUID } from "node:crypto";
import Fastify from "fastify";
import type { FastifyInstance } from "fastify";
import type { Logger } from "pino";
import type { CliRunner } from "./cli-runner.js";
import type { CallbackServerHandle } from "./internal-mcp-server.js";
import { createClaudeCliRunner } from "./cli-runner.js";
import type { CliAuthConfig } from "./cli-env.js";
import { runAgent, type AgentOutcome, type HostMcpServerInput } from "./agent-run.js";
import type { PostCallbackFn } from "./server.js";
import { defaultPostCallback } from "./server.js";
import type { PostAttributesFn } from "./attributes-tools.js";
import type { Observability } from "./observability.js";
import { mountObservability } from "./observability.js";
import { CliConfigError, isCliConfigError } from "./cli-config-error.js";
import type { McpCatalog } from "./mcp-catalog.js";

export interface HttpBridgeConfig {
  host: string;
  port: number;
  callback: CallbackServerHandle;
  cliRunner?: CliRunner;
  cliAuth?: CliAuthConfig;
  silenceTimeoutMs: number;
  logger: Logger;
  postCallback?: PostCallbackFn;
  postAttributes?: PostAttributesFn;
  mcpCatalog?: McpCatalog;
  mcpAllowInline?: boolean;
  observability?: Observability;
  observabilityHttpBridgeUrl?: string;
}

export interface RunningHttpBridge {
  address: string;
  shutdown(): Promise<void>;
}

interface ExecuteBody {
  node_id?: string;
  instance_id?: string;
  node_type?: string;
  dispatch_id?: string;
  attributes?: unknown;
  attributes_schema?: unknown;
  stores?: Record<string, unknown>;
  callback_url?: string;
  cancel_token?: string;
  resume_context?: {
    payload?: string;
    session_token?: string;
    resume_reason?: string;
  };
  prior_dispatch_id?: string;
  prior_dispatch_disposition?: string;
  run_scope_id?: string;
}

export async function startHttpBridge(
  config: HttpBridgeConfig,
): Promise<RunningHttpBridge> {
  const app: FastifyInstance = Fastify({
    logger: false,
  });
  const post = config.postCallback ?? defaultPostCallback;
  const cliRunner = config.cliRunner ?? createClaudeCliRunner({
    auth: requireAuth(config.cliAuth),
  });

  app.get("/healthz", async () => ({ ok: true }));

  if (config.observability) {
    mountObservability(
      app,
      config.observability,
      config.observabilityHttpBridgeUrl,
    );
  }

  app.post("/execute", async (req, reply) => {
    const body = (req.body ?? {}) as ExecuteBody;
    const ackId = randomUUID();
    const runId = body.dispatch_id && body.dispatch_id.length > 0
      ? body.dispatch_id
      : randomUUID();
    const traceId = body.dispatch_id && body.dispatch_id.length > 0
      ? body.dispatch_id
      : ackId;
    const log = config.logger.child({ run_id: runId, node_id: body.node_id });

    if (config.observability) {
      config.observability.recordEvent(traceId, {
        category: "step_started",
        attributes: {
          step_id: "dispatch",
          node_id: body.node_id,
          node_type: body.node_type,
        },
      });
    }

    void runAndCallback(body, ackId, traceId, runId, config, cliRunner, post, log);

    reply.code(202).send({ async_ack_id: ackId });
  });

  const bindHost = config.host;
  const addr = await app.listen({ host: bindHost, port: config.port });
  config.logger.info({ addr }, "claude-agent HTTP bridge listening");

  return {
    address: addr,
    shutdown: () => app.close(),
  };
}

async function runAndCallback(
  body: ExecuteBody,
  ackId: string,
  traceId: string,
  runId: string,
  config: HttpBridgeConfig,
  cliRunner: CliRunner,
  post: PostCallbackFn,
  logger: Logger,
): Promise<void> {
  try {
    const attributes = toRecord(body.attributes);
    const outcome = await runAgent({
      runId,
      nodeId: body.node_id ?? runId,
      nodeType: body.node_type ?? "unknown",
      model: stringOr(attributes.model, "claude-sonnet-4-5"),
      systemPrompt: stringOr(attributes.system_prompt, ""),
      userPrompt: stringOr(attributes.user_prompt, ""),
      attributesSchema: body.attributes_schema ?? {},
      attributes,
      stores: unwrapStores(body.stores ?? {}),
      cwdFromStore: stringOrUndefined(attributes.cwd_from_store),
      cwdOverride: stringOrUndefined(attributes.cwd),
      cliConfig: parseCliConfig(attributes.cli),
      mcpCatalog: config.mcpCatalog,
      mcpAllowInline: config.mcpAllowInline,
      dispatchId: body.dispatch_id ?? "",
      runScopeId: body.run_scope_id ?? "",
      priorDispatchId: body.prior_dispatch_id ?? "",
      priorDispatchDisposition: body.prior_dispatch_disposition ?? "",
      callbackUrl: body.callback_url ?? "",
      cancelToken: body.cancel_token ?? "",
      cliRunner,
      callback: config.callback,
      silenceTimeoutMs: config.silenceTimeoutMs,
      logger,
      postAttributes: config.postAttributes,
      sessionToken: stringOr(attributes.session_token, ""),
    });
    const cb = outcomeToCallbackBody(outcome, ackId);
    if (config.observability) {
      const cat = outcome.kind === "complete"
        ? "step_completed"
        : outcome.kind === "errored"
        ? "step_failed"
        : "step_completed";
      const attrs: Record<string, unknown> = { step_id: "dispatch" };
      if (outcome.kind === "errored") {
        attrs.error = outcome.errorClass;
      }
      config.observability.recordEvent(traceId, { category: cat, attributes: attrs });
      config.observability.markComplete(traceId);
    }
    if (body.callback_url) {
      await post(body.callback_url, cb, logger);
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
    if (body.callback_url) {
      await post(
        body.callback_url,
        {
          async_ack_id: ackId,
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

export function outcomeToCallbackBody(
  outcome: AgentOutcome,
  ackId: string,
): Record<string, unknown> {
  if (outcome.kind === "complete") {
    return {
      async_ack_id: ackId,
      success: {
        attributes_delta: outcome.attributesDelta,
        changed: outcome.changed,
        change_summary: outcome.changeSummary,
      },
    };
  }
  if (outcome.kind === "blocked") {
    return {
      async_ack_id: ackId,
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
    const delta: Record<string, unknown> = { ...(outcome.attributesDelta ?? {}) };
    if (outcome.sessionToken && outcome.sessionToken.length > 0) {
      delta.session_token = outcome.sessionToken;
    }
    if (Object.keys(delta).length > 0) {
      parkBody.attributes_delta = delta;
    }
    return {
      async_ack_id: ackId,
      park: parkBody,
    };
  }
  return {
    async_ack_id: ackId,
    error: {
      error_class: outcome.errorClass,
      payload: outcome.payload,
    },
  };
}

function requireAuth(auth: CliAuthConfig | undefined): CliAuthConfig {
  if (!auth) {
    throw new Error(
      "claude-agent: cliAuth is required when no cliRunner is supplied — pass auth from main.ts",
    );
  }
  return auth;
}

function unwrapStructValue(v: unknown): unknown {
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

function unwrapStruct(v: unknown): Record<string, unknown> {
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

function unwrapStores(stores: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(stores)) {
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
  return Object.keys(out!).length > 0 ? out : undefined;
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

