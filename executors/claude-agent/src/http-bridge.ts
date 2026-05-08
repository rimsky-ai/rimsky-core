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
import { runAgent, type AgentOutcome } from "./agent-run.js";
import type { PostCallbackFn } from "./server.js";
import { defaultPostCallback } from "./server.js";
import type { PostAttributesFn } from "./attributes-tools.js";
import type { Observability } from "./observability.js";
import { mountObservability } from "./observability.js";

/**
 * HTTP+JSON bridge. Callers that can't speak gRPC POST to `/execute` with an
 * ExecuteRequest-shaped body; the bridge returns `{ async_ack_id }`
 * immediately and posts the outcome to `callback_url` when the agent finishes.
 *
 * Spec: docs/specs/2026-04-27-stores-redesign-v2-design.md §12.3 — the bridge
 * mirrors the gRPC shape, with bodies keyed by `type`.
 *
 * This is primarily a debug / integration surface — rimsky supervisors
 * normally use the gRPC transport.
 */
export interface HttpBridgeConfig {
  host: string;
  port: number;
  callback: CallbackServerHandle;
  cliRunner?: CliRunner;
  /**
   * Auth config used when constructing the default CLI runner. Required
   * unless `cliRunner` is supplied (tests inject a fake runner).
   */
  cliAuth?: CliAuthConfig;
  silenceTimeoutMs: number;
  logger: Logger;
  postCallback?: PostCallbackFn;
  postAttributes?: PostAttributesFn;
  /**
   * Optional observability ledger. When provided, the HTTP bridge:
   *   - mounts /observability/v1/* routes from observability.ts
   *   - emits step_started/step_completed/error events around each
   *     /execute call so dashboards can fetch the trace via
   *     GET /observability/v1/trace/{ack_id}.
   */
  observability?: Observability;
  /**
   * Externally-reachable HTTP base URL for the dashboard. Surfaced in
   * the observability capabilities response so dashboards can dial
   * the bridge directly. When empty, the dashboard falls back to its
   * gRPC dispatch endpoint and HTTP-only routes will not work.
   */
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
  // Supervisor-supplied dispatch identifier. When present, the bridge
  // keys the trace ledger by this value so dashboards can fetch the
  // trace via GET /observability/v1/trace/{dispatch_id}. Falls back to
  // the freshly-minted ackId for non-rimsky callers.
  dispatch_id?: string;
  userdata?: unknown;
  attributes?: unknown;
  attributes_schema?: unknown;
  stores?: Record<string, unknown>;
  callback_url?: string;
  cancel_token?: string;
  // Field number 10 (`resumed`) is reserved on the wire under
  // stores-redesign-v2. Resume is universal — substrate-detected.
  run_attempt?: number;
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
    const runId = body.node_id ?? randomUUID();
    // The trace ledger is keyed by supervisor's dispatch_id when one
    // arrives in the body (production path); otherwise by the locally
    // minted ackId (debug / integration callers). This is what makes
    // dashboard `getTrace(dispatch_id)` resolve.
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
    const userdata = toRecord(body.userdata);
    const attributes = toRecord(body.attributes);
    const outcome = await runAgent({
      runId,
      nodeId: body.node_id ?? runId,
      nodeType: body.node_type ?? "unknown",
      model: stringOr(userdata.model, "claude-sonnet-4-5"),
      systemPrompt: stringOr(userdata.system_prompt, ""),
      userPromptTemplate: stringOr(userdata.user_prompt_template, ""),
      attributesSchema: body.attributes_schema ?? {},
      attributes,
      templateVars: {
        userdata,
        attributes,
      },
      stores: unwrapStores(body.stores ?? {}),
      cwdFromStore: stringOrUndefined(userdata.cwd_from_store),
      cwdOverride: stringOrUndefined(userdata.cwd),
      cliConfig: parseCliConfig(userdata.cli),
      callbackUrl: body.callback_url ?? "",
      cancelToken: body.cancel_token ?? "",
      cliRunner,
      callback: config.callback,
      silenceTimeoutMs: config.silenceTimeoutMs,
      logger,
      postAttributes: config.postAttributes,
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
    logger.error({ error: String(e) }, "agent run failed unexpectedly");
    if (config.observability) {
      config.observability.recordEvent(traceId, {
        category: "error",
        severity: "ERROR",
        attributes: { error: String(e) },
      });
      config.observability.markComplete(traceId);
    }
    if (body.callback_url) {
      await post(
        body.callback_url,
        {
          async_ack_id: ackId,
          type: "errored",
          error_class: "executor_internal_error",
          payload: { error: String(e) },
        },
        logger,
      ).catch(() => {});
    }
  }
}

function outcomeToCallbackBody(
  outcome: AgentOutcome,
  ackId: string,
): Record<string, unknown> {
  if (outcome.kind === "complete") {
    // Spec §12.3: HTTP+JSON bridge body keyed by `type`. Spec §12.2:
    // legacy `result` field retired in favour of `attributes_delta`.
    return {
      async_ack_id: ackId,
      type: "complete",
      attributes_delta: outcome.attributesDelta,
      changed: outcome.changed,
      change_summary: outcome.changeSummary,
    };
  }
  if (outcome.kind === "blocked") {
    return {
      async_ack_id: ackId,
      type: "blocked",
      reason: outcome.reason,
      context: outcome.context,
    };
  }
  return {
    async_ack_id: ackId,
    type: "errored",
    error_class: outcome.errorClass,
    payload: outcome.payload,
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

// @source: src/server.ts (unwrapStruct + unwrapStructValue + toRecord)
// Mirror of the gRPC server's userdata unwrap. The HTTP bridge usually
// receives plain JSON (no Struct envelope) but accepts the proto-Struct
// shape too so behavior stays consistent across transports.
function unwrapStructValue(v: unknown): unknown {
  if (v === null || v === undefined) return null;
  if (typeof v !== "object") return v;
  const o = v as Record<string, unknown>;
  const kind = typeof o.kind === "string" ? o.kind : undefined;
  if (kind === "stringValue") return typeof o.stringValue === "string" ? o.stringValue : "";
  if (kind === "numberValue") return typeof o.numberValue === "number" ? o.numberValue : 0;
  if (kind === "boolValue") return typeof o.boolValue === "boolean" ? o.boolValue : false;
  if (kind === "nullValue") return null;
  if (kind === "structValue") return unwrapStruct(o.structValue);
  if (kind === "listValue") {
    const lv = o.listValue as { values?: unknown[] } | undefined;
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

// @source: src/server.ts (unwrapStores)
// Mirror of the gRPC server's store-handle unwrap.
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

// @source: src/server.ts (parseCliConfig + helpers)
// Mirror of the gRPC path's userdata.cli reader.
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

function parseCliConfig(v: unknown): {
  bare?: boolean;
  permissionMode?: string;
  allowedTools?: string[];
  disallowedTools?: string[];
  addDirs?: string[];
  maxBudgetUsd?: string;
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
  return Object.keys(out!).length > 0 ? out : undefined;
}
