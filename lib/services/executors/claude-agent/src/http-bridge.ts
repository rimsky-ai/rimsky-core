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
   * Startup MCP-server catalog (S-executors-mcp-catalog-transports). Parsed
   * once at process startup from `RIMSKY_EXECUTOR_MCP_CATALOG`; threaded into
   * every dispatch's `runAgent` so a `cli.mcp_servers` `{ ref: }` resolves.
   */
  mcpCatalog?: McpCatalog;
  /**
   * `allow_inline` policy (default false) from
   * `RIMSKY_EXECUTOR_MCP_ALLOW_INLINE`. Gates whether inline
   * `cli.mcp_servers` entries are permitted at dispatch.
   */
  mcpAllowInline?: boolean;
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
  attributes?: unknown;
  attributes_schema?: unknown;
  stores?: Record<string, unknown>;
  callback_url?: string;
  cancel_token?: string;
  // Field number 10 (`resumed`) is reserved on the wire under
  // stores-redesign-v2. Resume is universal — substrate-detected.
  // Field number 11 (`run_attempt`) is reserved on the wire under
  // the 2026-05-20 per-run attribute keying spec — each dispatch has
  // a fresh dispatch_id; consumers keying on attempts use dispatch_id.
  // J10 plan: when this is a resume after the `Park` terminal, the
  // supervisor populates resume_context with the original
  // session_token + payload
  // and a resume_reason ("deadline_elapsed" | "external_invalidate").
  // The bridge extracts session_token and passes it to the CLI's
  // `--resume <id>` arg; payload + reason are exposed to the prompt
  // template as `{{rimsky.resume_payload}}` / `{{rimsky.resume_reason}}`.
  resume_context?: {
    payload?: string; // base64 of bytes; optional, may be empty
    session_token?: string;
    resume_reason?: string;
  };
  // Recovery-aware fields (per spec
  // .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
  // §Recovery-aware executor protocol).
  prior_dispatch_id?: string;
  prior_dispatch_disposition?: string;
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
    // Per the 2026-05-20 per-run keying refactor, the writeback URL's run_id
    // segment must equal the supervisor's dispatch_id so attributesAuth can
    // verify the cancel_token. The node_id is a poor proxy because it is
    // stable across runs of the same node. Fall back to a fresh UUID only
    // when no dispatch_id was supplied (debug / integration callers).
    const runId = body.dispatch_id && body.dispatch_id.length > 0
      ? body.dispatch_id
      : randomUUID();
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
      // Startup MCP catalog + allow_inline policy thread through so a node's
      // `cli.mcp_servers` `{ ref: }` resolves against the catalog at dispatch.
      mcpCatalog: config.mcpCatalog,
      mcpAllowInline: config.mcpAllowInline,
      // Raw dispatch_id (not runId): the sign-off gate binds to and
      // enforces non-emptiness on this, distinct from the UUID-fallback
      // runId above.
      dispatchId: body.dispatch_id ?? "",
      callbackUrl: body.callback_url ?? "",
      cancelToken: body.cancel_token ?? "",
      cliRunner,
      callback: config.callback,
      silenceTimeoutMs: config.silenceTimeoutMs,
      logger,
      postAttributes: config.postAttributes,
      resumeContext: parseResumeContext(body.resume_context),
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
    // A CliConfigError means a present-but-malformed cli.* config (e.g. a
    // required_signoffs entry missing public_key) — a host configuration
    // error, not an executor fault. Surface it as the declared
    // `agent/attribute_invalid` class so a misconfigured sign-off gate
    // fails LOUDLY (same fail-loud mode as the empty-dispatch_id path in
    // agent-run.ts) instead of silently degrading to an ungated run.
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
      // Same AsyncCallbackBody oneof shape the supervisor's
      // parseAsyncCallback requires (success | error | park); the legacy
      // `{type: ...}` discriminator is rejected with HTTP 400.
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

/**
 * encodeBase64 wraps a Uint8Array in a base64 string suitable for the
 * proto-JSON `bytes` field encoding (the convention Go's
 * `encoding/json.Unmarshal` uses to decode `[]byte` fields).
 */
function encodeBase64(bytes: Uint8Array): string {
  return Buffer.from(bytes).toString("base64");
}

// Projects the per-dispatch named-event buffer into the callback body's
// `events` slot (proto field 1 of AsyncCallbackBody). Each entry is
// `{name, payload}` with the payload base64-encoded — the proto-JSON
// convention for `bytes` fields the Go supervisor's
// `asyncCallbackNamedEvent.Payload []byte` decodes regardless of transport.
// An empty / absent buffer yields no `events` key.
//
// Exported for unit tests; not part of the agent-contract surface.
export function emittedEventsCallbackSlot(
  outcome: AgentOutcome,
): { events: { name: string; payload: string }[] } | Record<string, never> {
  const emitted = outcome.emittedEvents ?? [];
  if (emitted.length === 0) return {};
  return {
    events: emitted.map((e) => ({
      name: e.name,
      payload: encodeBase64(e.payload),
    })),
  };
}

// Exported for unit tests; not part of the agent-contract surface.
export function outcomeToCallbackBody(
  outcome: AgentOutcome,
  ackId: string,
): Record<string, unknown> {
  // The callback body uses the AsyncCallbackBody outcome-oneof shape
  // (success | error | park), identical to the gRPC variant in server.ts —
  // both POST to the same supervisor `/v1/callback/{ack}` endpoint, whose
  // parser (Go parseAsyncCallback) requires exactly one of success | error |
  // park and rejects the legacy `{type: ...}` / `{park_requested: ...}`
  // discriminator shape with HTTP 400. `async_ack_id` is carried in the body
  // for the HTTP bridge (the gRPC variant omits it — it rides the route
  // param); the supervisor reads the ack from the route and ignores the
  // body field, so it is harmless. Events ride the `events[]` array.
  //
  // @source server.ts::outcomeToCallbackBody (the gRPC sibling; same oneof
  // body, minus async_ack_id).
  const events = emittedEventsCallbackSlot(outcome);
  if (outcome.kind === "complete") {
    return {
      async_ack_id: ackId,
      ...events,
      success: {
        attributes_delta: outcome.attributesDelta,
        changed: outcome.changed,
        change_summary: outcome.changeSummary,
      },
    };
  }
  if (outcome.kind === "blocked") {
    // Post-E.2 collapse: `Blocked` maps to
    // `Error{error_class: "agent/blocked"}` (renamed 2026-05-23 per
    // signal-taxonomy spec, hierarchical-class convention).
    return {
      async_ack_id: ackId,
      ...events,
      error: {
        error_class: "agent/blocked",
        payload: { reason: outcome.reason, context: outcome.context },
      },
    };
  }
  if (outcome.kind === "park_requested") {
    // The proto-JSON convention for `bytes` fields is base64; Go's
    // `encoding/json.Unmarshal` into []byte expects a base64 string.
    return {
      async_ack_id: ackId,
      ...events,
      park: {
        reason: outcome.reason,
        reason_note: outcome.reasonNote ?? "",
        payload: encodeBase64(outcome.payload),
        ...(outcome.resumeAt ? { resume_at: outcome.resumeAt.toISOString() } : {}),
        session_token: outcome.sessionToken,
      },
    };
  }
  return {
    async_ack_id: ackId,
    ...events,
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

// @source: src/server.ts (unwrapStruct + unwrapStructValue + toRecord)
// Mirror of the gRPC server's attributes unwrap. The HTTP bridge usually
// receives plain JSON (no Struct envelope) but accepts the proto-Struct
// shape too so behavior stays consistent across transports. Both snake_case
// and camelCase Value-kind discriminators are accepted — matches the
// production gRPC path that uses keepCase: true.
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
// Mirror of the gRPC path's attributes.cli reader.
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
  // Sign-off gate: host-wired validator MCP servers and the required
  // (public_key, path) signature pairs. Type-shape-only validation,
  // like every other field here — rimsky never inspects the values.
  const ms = parseMcpServers(cli.mcp_servers);
  if (ms !== undefined) out!.mcpServers = ms;
  const rs = parseRequiredSignoffs(cli.required_signoffs);
  if (rs !== undefined) out!.requiredSignoffs = rs;
  const msa = numberOrUndefined(cli.max_signoff_attempts);
  if (msa !== undefined) out!.maxSignoffAttempts = msa;
  return Object.keys(out!).length > 0 ? out : undefined;
}

// @source: src/server.ts (parseMcpServers)
// Two entry shapes (S-executors-mcp-catalog-transports): a catalog reference
// `{ ref: <name> }` (resolved in agent-run.ts against the startup catalog) or
// an inline `{ name, url, headers, allowed_tools }` server (permitted only
// under the `allow_inline` policy, also enforced in agent-run.ts). A
// present-but-malformed entry throws CliConfigError rather than being
// silently dropped (mcp_servers wires the validator servers the sign-off
// gate depends on). Field-absent (`v` not an array) ⇒ undefined.
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

// @source: src/server.ts (parseRequiredSignoffs)
// A present-but-malformed entry (missing / non-string / empty public_key)
// throws CliConfigError rather than being silently dropped: required_signoffs
// is a security gate, and a dropped entry would silently weaken (or disable)
// enforcement. Field-absent (`v` not an array) ⇒ undefined (no gate).
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

// @source: src/server.ts (parseStringRecord)
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

// @source: src/server.ts (parseResumeContext)
function parseResumeContext(v: unknown): {
  payload?: Uint8Array;
  sessionToken?: string;
  resumeReason?: string;
} | undefined {
  if (!v || typeof v !== "object") return undefined;
  const r = v as Record<string, unknown>;
  const out: {
    payload?: Uint8Array;
    sessionToken?: string;
    resumeReason?: string;
  } = {};
  if (typeof r.payload === "string" && r.payload.length > 0) {
    out.payload = Buffer.from(r.payload, "base64");
  }
  if (typeof r.session_token === "string" && r.session_token.length > 0) {
    out.sessionToken = r.session_token;
  }
  if (typeof r.resume_reason === "string" && r.resume_reason.length > 0) {
    out.resumeReason = r.resume_reason;
  }
  return Object.keys(out).length > 0 ? out : undefined;
}
