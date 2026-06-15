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
import type { PostAttributesFn } from "./attributes-tools.js";
import type { Observability, TraceEvent } from "./observability.js";
import { expectedAttributesSchemaBytes, resolveDeclaredEvents, declaredErrorClasses } from "./expected-attributes-schema.js";
import { CliConfigError, isCliConfigError } from "./cli-config-error.js";
import type { McpCatalog } from "./mcp-catalog.js";

/**
 * Implements the gRPC Executor surface. Always responds with the async-handoff
 * pattern: one Heartbeat + StreamClose{AwaitAsyncCallback}, close stream,
 * run agent in background, POST final outcome to callback_url.
 *
 * Spec: docs/specs/2026-04-27-stores-redesign-v2-design.md §12.
 */
export interface GrpcServerConfig {
  host: string;
  port: number;
  callback: CallbackServerHandle;
  cliRunner?: CliRunner;
  /**
   * Auth config used when constructing the default CLI runner. Required
   * unless `cliRunner` is supplied (tests inject a fake runner that
   * doesn't spawn `claude`).
   */
  cliAuth?: CliAuthConfig;
  silenceTimeoutMs: number;
  logger: Logger;
  /**
   * Startup MCP-server catalog (S-executors-mcp-catalog-transports). Parsed
   * once at startup and threaded into every dispatch's `runAgent` so a node's
   * `cli.mcp_servers` `{ ref: }` resolves against the catalog.
   */
  mcpCatalog?: McpCatalog;
  /**
   * Inline-server policy (`allow_inline`, default false) gating whether inline
   * `cli.mcp_servers` entries are permitted at dispatch.
   */
  mcpAllowInline?: boolean;
  /**
   * Optional override of the HTTP POST function used to deliver the final
   * callback. Tests swap this out to avoid real network calls.
   */
  postCallback?: PostCallbackFn;
  /**
   * Optional override for the writeback POST used by the `attributes_set`
   * MCP tool. Threaded through to `runAgent`.
   */
  postAttributes?: PostAttributesFn;
  /**
   * Optional observability ledger. When provided, the gRPC Execute
   * handler:
   *   - records `step_started` on receipt, keyed by the supervisor's
   *     `dispatch_id` (or the locally minted ackId when absent)
   *   - records one of `step_completed` / `step_failed` /
   *     `step_blocked` / `step_parked` from the outcome path so each
   *     terminal kind shows up distinctly in dashboards
   * The same ledger instance is normally shared with the HTTP bridge so
   * dashboards can fetch traces via either transport.
   */
  observability?: Observability;
  /**
   * When non-empty, advertised via `ExecutorObservability.Capabilities`
   * as the externally-reachable HTTP+JSON observability bridge URL.
   * Mirrors the value exposed by `mountObservability(...)`.
   */
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
  /** Per-run typed attributes object (spec §5.7). The unified attribute
   * bag carries both rimsky-resolved inputs (`model`, `system_prompt`,
   * `user_prompt`, `cli.*`, ...) and executor-populated outputs.
   * Source-driven fields are pre-populated by rimsky at dispatch. */
  attributes?: unknown;
  /** Declared JSON Schema for `attributes` (spec §5.7.1). */
  attributes_schema?: unknown;
  /** Per-store handles keyed by store-config name (spec §12.1). Surfaced
   * to the agent via the attribute bag — no in-process interpretation. */
  stores?: Record<string, unknown>;
  callback_url?: string;
  cancel_token?: string;
  /** Supervisor-side rimsky_dispatch.id (proto field 12). Used by
   * the executor observability ledger as the per-dispatch trace key.
   *
   * Field number 10 (`resumed`) is reserved on the wire under
   * stores-redesign-v2 (proto reserves both number and name). Resume is
   * universal; the substrate detects resumed-vs-fresh internally.
   * Field number 11 (`run_attempt`) is reserved on the wire under the
   * 2026-05-20 per-run attribute keying spec — each dispatch has a
   * fresh dispatch_id; consumers keying on attempts use dispatch_id. */
  dispatch_id?: string;
  /** Resume context populated by the supervisor when this is a
   * resume after Park. session_token feeds the CLI's `--resume`
   * arg; payload + reason are template-visible vars. */
  resume_context?: {
    /** Base64 of bytes; optional, may be empty. */
    payload?: string;
    session_token?: string;
    resume_reason?: string;
  };
  /** Recovery-aware fields (per the 2026-05-22 fan-out safety
   * scope-first spec §Recovery-aware executor protocol). When this
   * dispatch supersedes a failed / heartbeat-stale / recalculated
   * predecessor for the same (run_scope_id, node_id), the supervisor
   * stamps the predecessor's dispatch_id here so the executor can
   * identify itself as a continuation. `prior_dispatch_disposition`
   * classifies why (`heartbeat_stale` | `retry_after_error` |
   * `recalculate`). Both fields are optional and unset on initial
   * dispatches. */
  prior_dispatch_id?: string;
  prior_dispatch_disposition?: string;
}

type GrpcCall = grpc.ServerWritableStream<ExecuteRequest, unknown>;

/**
 * @agent-contract
 * what: Constructs the gRPC server hosting `Executor.Execute`, binds to
 *   `host:port`, and wires the async-handoff agent run path.
 * how: `await startGrpcServer(config)`; `shutdown()` stops the server
 *   gracefully.
 * handles: stub-mode short-circuit; JSON-Schema validation of
 *   `attributes_delta` writes; silence / subprocess-exit fault mapping
 *   to a StreamClose Error outcome on the wire; bridging the
 *   supervisor's incremental writeback URL into the internal-MCP
 *   `attributes_set` tool.
 * does-not-handle: supervisor-side state transitions, commit, or on_error
 *   routing; those remain in the supervisor process.
 */
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
    Execute: (call: GrpcCall) => handleExecute(call, config, cliRunner, post),
  });

  // @deliberate: plan A1 — register the ExecutorObservability service so the rimsky
  // supervisor's discovery handshake succeeds against the gRPC endpoint
  // (otherwise the cached `expected_attributes_schema` and
  // `declared_events` are never populated and dispatch-time
  // effective-attribute-schema computation silently falls through). The
  // handlers bridge into the same `Observability` ledger the HTTP+JSON
  // routes expose.
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
        declared_events: resolveDeclaredEvents(),
        // @deliberate: 2026-05-23 signal-taxonomy Pass 6: hierarchical error vocabulary.
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
        // @deliberate: mirror GetTrace's evicted-shape close.
        call.write(traceEventToProto({
          event_id: randomUUID(),
          timestamp: new Date().toISOString(),
          severity: "INFO",
          category: "trace_complete",
        }));
        call.end();
        return;
      }
      // @deliberate: spec §2.5: idle-close after RIMSKY_OBS_IDLE_TIMEOUT_MS (default
      // 5 minutes). Without this, a StreamTrace request for an unknown
      // dispatch_id would create a fresh empty record (`complete:false`,
      // not yet evicted) and pin server-side resources indefinitely
      // since no events would ever arrive to drive a `trace_complete`.
      // Mirror the HTTP+JSON sibling's `armIdle()` protection in
      // `observability.ts::mountObservability`.
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
  // @deliberate: grpc-js 1.10+: server starts listening automatically after bindAsync; the
  // prior `server.start()` call is a deprecated no-op in recent versions.

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
  call: GrpcCall,
  config: GrpcServerConfig,
  cliRunner: CliRunner,
  post: PostCallbackFn,
): void {
  const req = call.request;
  const ackId = randomUUID();
  // @deliberate: per the 2026-05-20 per-run keying refactor, the writeback URL's run_id
  // segment must equal the supervisor's dispatch_id so attributesAuth can
  // verify the cancel_token. The node_id is a poor proxy because it is
  // stable across runs of the same node — multiple dispatches of the same
  // node would collide in trace ledgers and reuse a stale run_id at
  // writeback. Fall back to a fresh UUID only when no dispatch_id was
  // supplied (stub-mode probes, ad-hoc unit tests).
  const runId = req.dispatch_id && req.dispatch_id.length > 0
    ? req.dispatch_id
    : randomUUID();
  // @deliberate: trace ledger key: prefer the supervisor-supplied dispatch_id so
  // dashboards can fetch traces by it (proto field 12). When absent
  // (stub-mode probes, ad-hoc unit tests), fall back to the ackId.
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
      stores: Object.keys(req.stores ?? {}),
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

  // @deliberate: 1) Heartbeat + StreamClose{AwaitAsyncCallback}, then close the stream.
  // Post-spec:2026-05-12 (Group E.4): the wire is StreamClose + outcome
  // oneof; AsyncAccepted is renamed AwaitAsyncCallback.
  call.write({
    heartbeat: {
      timestamp_ms: Date.now(),
      note: "accepted",
    },
  });
  call.write({
    stream_close: {
      await_async: {
        async_ack_id: ackId,
        expected_completion_ms: 0,
      },
    },
  });
  call.end();

  // @deliberate: 2) Run the agent in the background; deliver final outcome via HTTP POST.
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
  // @deliberate: fast-fail dispatches (CliConfigError thrown by resolveHostServers,
  // dispatch_id missing, malformed attributes) settle in single-digit
  // milliseconds. The supervisor registers the async ack id AFTER
  // draining the gRPC stream — a sequence that takes tens of
  // milliseconds. Without this defensive yield, the fast-fail callback
  // POST races ahead of the registration, the supervisor responds 404
  // (unknown ack id), the supervisor never receives the failure
  // terminal, and the node hangs in `running` until the heartbeat-loss
  // sweep re-dispatches into the same race — looping forever. Slow
  // (CLI-spawning) dispatches don't hit this because the spawn itself
  // takes longer than the registration window. Property protected:
  // dispatch-time failures land on the FIRST callback POST, not via the
  // heartbeat-loss sweep.
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
      stores: unwrapStores(req.stores ?? {}),
      cwdFromStore: stringOrUndefined(attributes.cwd_from_store),
      cwdOverride: stringOrUndefined(attributes.cwd),
      cliConfig: parseCliConfig(attributes.cli),
      // @deliberate: startup MCP catalog + allow_inline policy thread through so a node's
      // `cli.mcp_servers` `{ ref: }` resolves against the catalog at dispatch.
      mcpCatalog: config.mcpCatalog,
      mcpAllowInline: config.mcpAllowInline,
      // @deliberate: raw dispatch_id (not runId): the sign-off gate binds to and
      // enforces non-emptiness on this, distinct from the UUID-fallback
      // runId above.
      dispatchId: req.dispatch_id ?? "",
      callbackUrl: req.callback_url ?? "",
      cancelToken: req.cancel_token ?? "",
      cliRunner,
      callback: config.callback,
      silenceTimeoutMs: config.silenceTimeoutMs,
      logger,
      postAttributes: config.postAttributes,
      resumeContext: resolveEffectiveResumeContext(
        parseResumeContext(req.resume_context),
        attributes,
      ),
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
    // @deliberate: A CliConfigError means a present-but-malformed cli.* config (e.g. a
    // required_signoffs entry missing public_key) — a host configuration error, not an
    // executor fault. Surface it as the declared `agent/attribute_invalid` class so a
    // misconfigured sign-off gate fails LOUDLY (same fail-loud mode as the
    // empty-dispatch_id path in agent-run.ts) instead of silently degrading to an
    // ungated run.
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

/**
 * Build the full callback URL. The supervisor-supplied `callback_url` is the
 * base address of its callback server (e.g. `http://supervisor:9100`); the
 * per-async path `/v1/callback/{async_ack_id}` is appended here so the Go
 * supervisor's chi router can extract the ack ID from the URL path.
 */
function buildCallbackUrl(base: string, ackId: string): string {
  const trimmed = base.replace(/\/+$/, "");
  return `${trimmed}/v1/callback/${encodeURIComponent(ackId)}`;
}

/**
 * Wraps a Uint8Array in a base64 string suitable for the proto-JSON
 * `bytes` field encoding (the convention Go's `encoding/json.Unmarshal`
 * uses to decode `[]byte` fields). Used by both transports — keep in sync
 * with http-bridge.ts.
 */
function encodeBase64(bytes: Uint8Array): string {
  return Buffer.from(bytes).toString("base64");
}

/**
 * Projects the per-dispatch named-event buffer into the AsyncCallbackBody
 * `events` slot (proto field 1). Each entry is `{name, payload}` with the
 * payload base64-encoded — the proto-JSON convention for `bytes` fields, as
 * the Go supervisor's `asyncCallbackNamedEvent.Payload []byte` expects. An
 * empty / absent buffer yields no `events` key (no behavior change for
 * agents that emit nothing).
 *
 * Exported for unit tests; not part of the agent-contract surface.
 */
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

/**
 * Exported for unit tests; not part of the agent-contract surface.
 */
export function outcomeToCallbackBody(
  outcome: AgentOutcome,
): Record<string, unknown> {
  // @deliberate: the callback body uses the AsyncCallbackBody outcome-oneof shape
  // (success | error | park), optionally preceded by an `events[]` stream
  // replayed before the outcome verdict. The legacy `{type: ...}`
  // discriminator is no longer accepted by the supervisor.
  const events = emittedEventsCallbackSlot(outcome);
  if (outcome.kind === "complete") {
    return {
      ...events,
      success: {
        attributes_delta: outcome.attributesDelta,
        changed: outcome.changed,
        change_summary: outcome.changeSummary,
      },
    };
  }
  if (outcome.kind === "blocked") {
    // @deliberate: post-E.2 collapse: `Blocked` maps to
    // `Error{error_class: "agent/blocked"}` (renamed 2026-05-23 per
    // signal-taxonomy spec, hierarchical-class convention).
    return {
      ...events,
      error: {
        error_class: "agent/blocked",
        payload: { reason: outcome.reason, context: outcome.context },
      },
    };
  }
  if (outcome.kind === "park_requested") {
    // @deliberate: the proto-JSON convention for `bytes` fields is base64 — Go's
    // `encoding/json` decodes []byte fields from base64 strings.
    //
    // The supervisor consumes `reason` as the closed two-value
    // ParkReason projection (await_callback | snooze) per spec
    // .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
    // §ParkReason collapse. `reason_note` is the free-form human
    // annotation; inert in rimsky.
    return {
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
    ...events,
    error: {
      error_class: outcome.errorClass,
      payload: outcome.payload,
    },
  };
}

// @deliberate: unwraps a google.protobuf.Struct value (as decoded by @grpc/proto-loader
// with the default options: { fields: { [key]: Value } }) into a plain
// object. Without this, downstream lookups like `attributes.model` see
// `undefined` because the actual value is at `attributes.fields.model.stringValue`.
// Unwrap a google.protobuf.Value carried over the wire by @grpc/proto-loader.
// Accepts both shapes proto-loader can produce:
//   - keepCase: true → {kind: "string_value", string_value: "x"} (snake_case;
//     this is the production setting, see proto-loader.ts).
//   - keepCase: false → {kind: "stringValue", stringValue: "x"} (camelCase).
//   - older fixtures that omit the kind discriminator and present the
//     value field directly.
// Reading both forms keeps server.ts's type narrowing correct regardless of
// which encoding the caller used (production gRPC vs hand-rolled test fixtures).
// Exported for unit tests; not part of the agent-contract surface.
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

/**
 * Exported for unit tests; not part of the agent-contract surface.
 */
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
  // @deliberate: google.protobuf.Struct shape from @grpc/proto-loader: a top-level
  // `fields` map of Value-typed entries with a `kind` discriminator.
  // Plain object shape is also accepted (e.g. from in-process tests).
  if ("fields" in v && typeof (v as { fields?: unknown }).fields === "object") {
    return unwrapStruct(v);
  }
  return v as Record<string, unknown>;
}

/**
 * Unwraps the per-store `StoreHandle.handle` Struct into a plain object
 * so downstream consumers (resolveCwd, attribute substitution) can read
 * `stores[alias].handle.address` as a string. Without this, `.handle` is
 * the raw Struct shape and `.handle.address` is `undefined`.
 */
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

/**
 * Parses the `attributes.cli` sub-object into the typed shape consumed
 * by runAgent + cliRunner. Returns `undefined` when the input is
 * missing or empty, so the executor's defaults (current behavior) take
 * effect.
 *
 * Field-to-spawn-arg mapping is documented on `CliSpawnRequest` in
 * cli-runner.ts; rimsky never inspects the values, so the validation
 * here is type-shape-only.
 */
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
  // @deliberate: attributes.cli.handle_rate_limits — default true (J9). Explicit
  // false disables the auto-park behavior.
  const hr = boolOrUndefined(cli.handle_rate_limits);
  if (hr !== undefined) out!.handleRateLimits = hr;
  const msc = numberOrUndefined(cli.max_schema_corrections);
  if (msc !== undefined) out!.maxSchemaCorrections = msc;
  // @deliberate: sign-off gate: host-wired validator MCP servers and the required
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

// @source: lib/services/executors/claude-agent/src/server.ts (parseMcpServers) — mirrored in http-bridge.ts.
// Parses cli.mcp_servers into the executor's host-server shape. Two entry
// shapes (S-executors-mcp-catalog-transports):
//   - `{ ref: <name> }` — a catalog reference resolved at dispatch against
//     the startup catalog. Only the non-empty string `ref` is validated
//     here; the referenced transport (http/stdio/module/http-loopback) is
//     resolved in agent-run.ts.
//   - inline `{ name, url, headers, allowed_tools }` — a server declared on
//     the node. Permitted only when the `allow_inline` policy is true
//     (enforced in agent-run.ts); the shape is still validated here.
// A present-but-malformed entry throws CliConfigError rather than being
// silently dropped: mcp_servers wires the validator servers the sign-off
// gate depends on, so a dropped entry could unwire a validator the host
// intended the agent to reach. Field-absent (`v` not an array) ⇒ undefined.
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
    // @deliberate: catalog reference shape.
    if ("ref" in e) {
      if (typeof e.ref !== "string" || e.ref.length === 0) {
        throw new CliConfigError(
          `cli.mcp_servers[${i}].ref must be a non-empty string`,
        );
      }
      out.push({ ref: e.ref });
      continue;
    }
    // @deliberate: inline server shape.
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

// @source: lib/services/executors/claude-agent/src/server.ts (parseRequiredSignoffs) — mirrored in http-bridge.ts.
// Parses cli.required_signoffs, mapping snake_case public_key → publicKey
// and carrying through the optional path. A present-but-malformed entry
// (missing / non-string / empty public_key) throws CliConfigError rather
// than being silently dropped: required_signoffs is a security gate, and a
// dropped entry would silently weaken (or disable) enforcement, letting
// unsigned output resolve to terminal success. Field-absent (`v` not an
// array) ⇒ undefined (no gate).
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

// @source: lib/services/executors/claude-agent/src/server.ts (parseStringRecord) — mirrored in http-bridge.ts.
// Parses a flat string→string map (e.g. mcp_servers[].headers). Non-string
// values are dropped; an empty/absent map ⇒ undefined.
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

/**
 * Parse the supervisor-supplied resume_context payload into the shape
 * runAgent consumes. Returns undefined when no resume context is set
 * (fresh dispatch). Per J10.
 */
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

/**
 * Resolves the effective resume context for the current dispatch. The
 * supervisor-provided ResumeContext (req.resume_context) is the Park
 * path and wins when set. Otherwise, when the carry-forward
 * `session_token` attribute is non-empty, synthesize an attribute-
 * driven resume context so the CLI continues the prior conversation.
 *
 * Per the 2026-06-14 carry-forward design — the two paths are
 * independent: the Park path's session_token comes from the prior
 * Park terminal; the attribute path's session_token comes from the
 * prior dispatch's attribute writeback. Sub-graph invocations =
 * empty session_token = fresh CLI conversation.
 */
function resolveEffectiveResumeContext(
  fromParkPath: { payload?: Uint8Array; sessionToken?: string; resumeReason?: string } | undefined,
  attributes: Record<string, unknown>,
): { payload?: Uint8Array; sessionToken?: string; resumeReason?: string } | undefined {
  if (fromParkPath && fromParkPath.sessionToken && fromParkPath.sessionToken.length > 0) {
    return fromParkPath;
  }
  const fromAttribute = stringOr(attributes.session_token, "");
  if (fromAttribute.length === 0) {
    return fromParkPath;
  }
  return {
    payload: new Uint8Array(),
    sessionToken: fromAttribute,
    resumeReason: "carry_forward",
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

/**
 * Default callback poster. Uses Node's built-in `fetch`. Failures are logged
 * but not retried here; the supervisor's heartbeat-loss sweep will reclaim
 * the node if the callback never arrives.
 */
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

/**
 * Converts an `Observability` ledger TraceEvent into the wire shape the
 * proto-loader-generated `ExecutorObservability` service expects. The
 * loader's `enums: String` option means `severity` stays as the proto
 * constant name. `timestamp` is a `google.protobuf.Timestamp`
 * `{seconds, nanos}` pair; `attributes` is a `google.protobuf.Struct`
 * with a recursive `Value` envelope per field.
 *
 * Exported for unit tests; not part of the agent-contract surface.
 */
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

/**
 * Exported for unit tests; not part of the agent-contract surface.
 */
export function isoToProtoTimestamp(iso: string): { seconds: string; nanos: number } {
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return { seconds: "0", nanos: 0 };
  // @deliberate: google.protobuf.Timestamp requires `nanos` ∈ [0, 999_999_999] and
  // `seconds` to be the floor of the wall time. Using `Math.trunc` would
  // produce a negative `nanos` (and a non-floor `seconds`) for pre-epoch
  // sub-second inputs (e.g. -500ms → trunc=0, nanos=-500_000_000), which
  // violates the proto contract. `Math.floor` keeps the remainder in the
  // valid range for both positive and negative inputs.
  const seconds = Math.floor(ms / 1000);
  const nanos = (ms - seconds * 1000) * 1_000_000;
  return { seconds: seconds.toString(), nanos };
}

/**
 * Exported for unit tests; not part of the agent-contract surface.
 */
export function jsToProtoStruct(value: unknown): { fields: Record<string, unknown> } | null {
  if (value === null || value === undefined) return null;
  if (typeof value !== "object" || Array.isArray(value)) return null;
  const fields: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
    fields[k] = jsToProtoValue(v);
  }
  return { fields };
}

/**
 * Exported for unit tests; not part of the agent-contract surface.
 */
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
