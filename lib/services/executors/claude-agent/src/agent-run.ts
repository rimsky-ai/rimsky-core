// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { randomUUID } from "node:crypto";
import { statSync } from "node:fs";
import type { Logger } from "pino";
// ajv ships CJS — under ESM+NodeNext we reach the constructor through the
// interop namespace; the `.default` arm handles the nested form.
import * as AjvNs from "ajv";
type AjvCtor = new (opts?: object) => { compile: (schema: object) => (v: unknown) => boolean };
const Ajv: AjvCtor = (((AjvNs as unknown) as { default?: AjvCtor }).default ??
  ((AjvNs as unknown) as AjvCtor));
import type { CliRunner, CliHandle, CliToolConfig } from "./cli-runner.js";
import { startInternalMcpServer, type CallbackServerHandle } from "./internal-mcp-server.js";
import {
  type McpCatalog,
  resolveCatalogServer,
} from "./mcp-catalog.js";
import { CliConfigError } from "./cli-config-error.js";
import { resolveHeaderEnvRefs } from "./env-refs.js";
import {
  buildAttributesWritebackUrl,
  defaultPostAttributes,
  type PostAttributesFn,
} from "./attributes-tools.js";
import { detectRateLimit } from "./rate-limit.js";
import { classifyAgentError } from "./error-classify.js";
import { resolveDeclaredEvents } from "./expected-attributes-schema.js";
import { verifyRequiredSignoffs } from "./signoff.js";
import type { NamedEventEmission } from "./token-registry.js";

/**
 * Outcome the executor relays back to the rimsky supervisor via the async
 * callback URL. Per spec §12.2 the legacy `result` field has been retired in
 * favour of `attributes_delta`.
 *
 * - `complete`: terminal success — maps to a StreamClose `Success` outcome on
 *   the wire. `attributesDelta` is the terminal-final writeback (may be `null`
 *   when the executor used the incremental `attributes_set` callback path; the
 *   supervisor already has that data).
 * - `blocked`: maps to a StreamClose `Error{error_class:"agent/blocked"}`
 *   outcome on the wire (post-E.2 the pre-rename Blocked variant collapsed
 *   into Error with the reserved `agent/blocked` class; 2026-05-23 the
 *   class moved under the hierarchical `agent/*` prefix per the
 *   signal-taxonomy spec).
 * - `errored`: maps to a StreamClose `Error{error_class}` outcome on the wire.
 *
 * @source rimsky/src/supervisor/agentic-runner.ts (semantic port)
 */
/**
 * Non-terminal named events the agent emitted via the `emit_named_event`
 * MCP tool during the dispatch. Rides the async-callback body's `events[]`
 * array (the gRPC stream already closed at dispatch, so events cannot ride
 * it). Absent / empty when the agent emitted nothing. Threaded onto every
 * outcome variant so `outcomeToCallbackBody` can read it uniformly.
 */
export type AgentOutcomeBase = {
  emittedEvents?: NamedEventEmission[];
};

export type AgentOutcome = AgentOutcomeBase &
  (
  | {
      kind: "complete";
      attributesDelta: Record<string, unknown> | null;
      changed: boolean;
      changeSummary: string | null;
    }
  | { kind: "blocked"; reason: string; context: unknown }
  | { kind: "errored"; errorClass: string; payload: unknown }
  | {
      // J9 rate-limit auto-park (and any other voluntary park trigger).
      // The supervisor receives the `Park` terminal via the gRPC stream
      // or async callback (see plan A3).
      //
      // `reason` is the typed ParkReason snake_case value from the
      // closed two-value set (await_callback | snooze) per spec
      // .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
      // §ParkReason collapse. `reasonNote` is the free-form
      // annotation (`col:rimsky_node_runs.parked_reason_note`). The
      // MCP `report_park` tool resolves this same outcome shape; the
      // rate-limit detection path emits `reason: snooze` (deadline-
      // based wake via SweepParkedNodes) with a descriptive
      // `reasonNote`.
      kind: "park_requested";
      reason: string;
      reasonNote: string;
      payload: Uint8Array;
      resumeAt: Date | null; // null → indefinite park
      sessionToken: string;
    }
  );

/**
 * One entry in a node's `cli.mcp_servers`. Either an inline server declared
 * directly on the node (`{ name, url, … }`) or a catalog reference
 * (`{ ref: <name> }`) resolved at dispatch against the startup catalog.
 * S-executors-mcp-catalog-transports.
 */
export type HostMcpServerInput =
  | {
      name: string;
      url: string;
      headers?: Record<string, string>;
      allowedTools?: string[];
    }
  | { ref: string };

export interface AgentRunOptions {
  runId: string;
  /**
   * Supervisor-side `node_id` — denormalized for forensic queries. The path
   * segment on the writeback URL is `run_id` (= dispatch_id), per the
   * 2026-05-20 per-run keying refactor.
   */
  nodeId: string;
  nodeType: string;
  model: string;
  /**
   * The fully rimsky-resolved system prompt. Post-2026-05-21 userdata
   * collapse this is consumed verbatim — no executor-side template
   * rendering pass. Rimsky resolves substitutions at dispatch via
   * `code:graph/attribute/substitution.go::SubstituteValue`.
   */
  systemPrompt: string;
  /**
   * The fully rimsky-resolved user prompt. Post-2026-05-21 userdata
   * collapse this is consumed (with a fixed metadata-footer appended)
   * — no executor-side template rendering pass.
   */
  userPrompt: string;
  /**
   * Declared JSON Schema for the node's attributes. The executor uses this
   * to validate any `attributes_delta` it produces locally; rimsky validates
   * authoritatively at commit (per spec §5.7.1).
   */
  attributesSchema: unknown;
  /**
   * Per-run typed attributes object as captured at dispatch (per spec
   * §5.7). Includes both source-driven fields (pre-populated by the
   * supervisor) and any executor-populated fields preserved from a prior
   * resumable run. Surfaced verbatim to the agent via the `attributes_read`
   * MCP tool.
   */
  attributes: Record<string, unknown>;
  /**
   * Per-store handles delivered in `ExecuteRequest.stores` (spec §19.1).
   * Keyed by store-config name; each entry is the unwrapped
   * `{kind, handle: {address, payload, alias, intent}}` shape. Opaque
   * to rimsky; the executor unwraps per its store-specific knowledge.
   */
  stores?: Record<string, unknown>;
  /**
   * Optional store-config name from `attributes.cwd_from_store`. When set,
   * the executor reads `stores[<name>].handle.address` (which the
   * filesystem store fills with an absolute path) and uses it as the
   * spawned CLI's cwd. Validated as an existing directory before spawn;
   * any mismatch errors as `agent/attribute_invalid`.
   */
  cwdFromStore?: string;
  /**
   * Optional raw cwd from `attributes.cwd`. Override-of-last-resort for
   * deployments that pin a static workdir without going through a store.
   * Lower priority than `cwdFromStore`.
   */
  cwdOverride?: string;
  /**
   * Per-template CLI tuning sourced from `attributes.cli.*`. Forwarded
   * verbatim to {@link CliRunner.spawn} so the executor (not rimsky)
   * decides how each field maps to spawn args. See CliSpawnRequest
   * for the mapping. All optional; absence preserves current defaults.
   */
  cliConfig?: {
    bare?: boolean;
    permissionMode?: string;
    allowedTools?: string[];
    disallowedTools?: string[];
    addDirs?: string[];
    maxBudgetUsd?: string;
    /**
     * J9 plan: when true (default), claude-agent inspects CLI stderr for
     * rate-limit signals (Anthropic 429 / `rate_limit_error`) and emits
     * `park_requested` instead of `errored` so the supervisor parks the
     * node and resumes after the reset window.
     */
    handleRateLimits?: boolean;
    /**
     * J8 plan: maximum corrective `report_complete` retries on schema-
     * validation failure. The executor returns "rejected" with the
     * validation errors to the agent's MCP call, the agent corrects
     * and retries; after this many failed retries the run terminates
     * with a StreamClose `Error{error_class: "agent/schema_violation"}`
     * outcome on the wire. Default 3.
     */
    maxSchemaCorrections?: number;
    /**
     * Host-wired MCP servers (`cli.mcp_servers`). Each entry is appended to
     * the spawned CLI's `--mcp-config` and its tools are auto-allowed; the
     * sign-off gate's signers (`requiredSignoffs`) are typically — but not
     * necessarily — among these servers.
     *
     * Two entry shapes (S-executors-mcp-catalog-transports):
     *   - inline `{ name, url, headers, allowedTools }` — a server declared
     *     directly on the node. Permitted only when `mcpAllowInline` is true;
     *     rejected with a config error otherwise.
     *   - `{ ref: <name> }` — a reference resolved at dispatch against the
     *     startup `mcpCatalog`. The catalog entry's transport (http / stdio /
     *     module / http-loopback) determines the emitted `--mcp-config` leaf.
     */
    mcpServers?: HostMcpServerInput[];
    /**
     * The sign-off gate (`cli.required_signoffs`): each `{publicKey, path}`
     * must be satisfied by a valid Ed25519 signature in `report_complete`'s
     * `signoffs` bag before the dispatch can resolve to terminal success.
     */
    requiredSignoffs?: { publicKey: string; path?: string }[];
    /**
     * Maximum corrective `report_complete` retries when the sign-off gate
     * is unmet, mirroring `maxSchemaCorrections`. On exhaustion the run
     * terminal-errors with `agent/signoff_unobtained`. Default 3.
     */
    maxSignoffAttempts?: number;
  };
  /**
   * Startup MCP-server catalog (S-executors-mcp-catalog-transports). A
   * node's `cli.mcp_servers` entry of the form `{ ref: <name> }` resolves
   * against this map at the `hostServers` build site. Parsed once at startup
   * and threaded through unchanged. Absent ⇒ no catalog (a `{ ref: }` then
   * fails to resolve with a config error).
   */
  mcpCatalog?: McpCatalog;
  /**
   * `allow_inline` policy (default false). When false, an inline
   * `cli.mcp_servers` entry (`{ name, url }`, not a `{ ref: }`) is rejected
   * at dispatch with a config error — the catalog is the authoritative
   * server source. When true, inline servers are permitted alongside refs.
   */
  mcpAllowInline?: boolean;
  /**
   * Raw `ExecuteRequest.dispatch_id`; the sign-off gate binds to this and
   * requires it non-empty — distinct from `runId`, which falls back to a
   * random UUID. Binding to the raw field (not `runId`) is what makes the
   * empty-`dispatch_id` requirement enforceable and the per-dispatch
   * anti-replay property hold.
   */
  dispatchId?: string;
  /**
   * Supervisor-issued URLs / tokens that flow through to the incremental
   * writeback path and the async-handoff callback.
   */
  callbackUrl: string;
  cancelToken: string;
  cliRunner: CliRunner;
  callback: CallbackServerHandle;
  silenceTimeoutMs: number;
  logger: Logger;
  /**
   * J10: Resume context populated by the supervisor when this dispatch
   * is a resume after a prior `Park` terminal. When `sessionToken` is
   * non-empty, claude-agent launches the CLI with `--resume <token>`
   * so the prior conversation resumes; `payload` and `resumeReason`
   * are surfaced to the agent by appending a fixed `---`-delimited
   * metadata footer to the user prompt (see the prompt-assembly block
   * in `runAgentReal` below, ~lines 312-334). No template substitution
   * runs — rimsky already resolved prompt attributes at dispatch.
   */
  resumeContext?: {
    payload?: Uint8Array;
    sessionToken?: string;
    resumeReason?: string;
  };
  /**
   * Optional override for the writeback POST function used by the
   * `attributes_set` MCP tool. Tests swap this out to avoid real network
   * calls.
   */
  postAttributes?: PostAttributesFn;
}

export async function runAgent(opts: AgentRunOptions): Promise<AgentOutcome> {
  // Protocol contract: executors must reject malformed attribute bags
  // consistently across stub and live modes. The probe escape hatch
  // (`stub_probe: true`) is *only* honored in stub mode; conformance
  // scenarios that exercise malformed-shape rejection deliberately
  // omit the flag so the heuristic fires.
  const attrs = opts.attributes ?? {};
  const isProbe = attrs.stub_probe === true && stubModeEnabled();
  if (!isProbe) {
    const reason = malformedAttributesReason(attrs);
    if (reason !== null) {
      return {
        kind: "errored",
        errorClass: "agent/attribute_invalid",
        payload: { reason },
      };
    }
  }
  if (stubModeEnabled()) {
    return runAgentStub(opts);
  }
  return runAgentReal(opts);
}

export function stubModeEnabled(): boolean {
  return process.env.RIMSKY_EXECUTOR_STUB_MODE === "1";
}

async function runAgentStub(opts: AgentRunOptions): Promise<AgentOutcome> {
  opts.logger.info({ runId: opts.runId }, "agent-run: stub mode");
  await new Promise((r) => setTimeout(r, 50));
  const attrs = opts.attributes ?? {};

  // Conformance-probe escape hatch (mirrors http-node/server.go): when the
  // suite flags attributes with `stub_probe: true`, return either the
  // configured stub_response or the canonical {stub: true}. Malformed-
  // attribute rejection runs in `runAgent` before this function is
  // reached, so non-probe attributes that arrived here are known good.
  const isProbe = attrs.stub_probe === true;
  if (!isProbe) {
    // Stub-mode fallthrough for non-probe attributes: honor the §14.4
    // contract by returning the canonical stub response.
    return {
      kind: "complete",
      attributesDelta: { stub: true },
      changed: true,
      changeSummary: "stub",
    };
  }

  const stubResponse = attrs.stub_response;
  if (stubResponse !== undefined) {
    if (typeof stubResponse !== "object" || stubResponse === null || Array.isArray(stubResponse)) {
      return {
        kind: "errored",
        errorClass: "agent/attribute_invalid",
        payload: { reason: `stub_response must be a JSON object, got ${typeof stubResponse}` },
      };
    }
    return {
      kind: "complete",
      attributesDelta: stubResponse as Record<string, unknown>,
      changed: true,
      changeSummary: "stub",
    };
  }
  return {
    kind: "complete",
    attributesDelta: { stub: true },
    changed: true,
    changeSummary: "stub",
  };
}

// malformedAttributesReason returns a non-null reason string when the
// attribute bag matches a known "malformed" shape the conformance
// `malformed_attributes` scenario uses. Keep this list aligned with the
// scenario in `conformance/scenarios/malformed_attributes.go`.
//
// Reserved-key convention: malformed-shape markers MUST be `_`-prefixed
// (`_invalid`, `_missing_url`, …) so plain field names a template
// author might legitimately use cannot trip the rejection.
function malformedAttributesReason(attrs: Record<string, unknown>): string | null {
  if (attrs._invalid !== undefined) return "attributes._invalid present (reserved)";
  if (attrs._missing_url === true) return "attributes._missing_url is set (reserved)";
  return null;
}

async function runAgentReal(opts: AgentRunOptions): Promise<AgentOutcome> {
  const {
    runId,
    nodeId,
    model,
    systemPrompt,
    userPrompt,
    attributesSchema,
    attributes,
    stores,
    cwdFromStore,
    cwdOverride,
    cliConfig,
    mcpCatalog,
    mcpAllowInline,
    dispatchId,
    callbackUrl,
    cancelToken,
    cliRunner,
    silenceTimeoutMs,
    logger,
    postAttributes,
    resumeContext,
  } = opts;

  const cwdResolution = resolveCwd({
    stores: stores ?? {},
    cwdFromStore,
    cwdOverride,
  });
  if (cwdResolution.kind === "error") {
    return {
      kind: "errored",
      errorClass: "agent/attribute_invalid",
      payload: { error: cwdResolution.message },
    };
  }
  const cwd = cwdResolution.cwd;

  // Generate the per-run callback token. The agent (Claude Code CLI
  // subprocess) needs this token to call any rimsky-callback MCP tool.
  // Post-2026-05-21 userdata collapse the executor no longer runs a
  // template-rendering pass against the prompts — rimsky resolved the
  // prompt attributes at dispatch. The token + resume metadata are
  // delivered to the agent via a fixed `---`-delimited metadata footer
  // appended to the user prompt only (the system prompt stays clean to
  // preserve prompt caching: per-run mutable content invalidates the
  // cache).
  const callbackToken = randomUUID();
  const resumePayload = resumeContext?.payload && resumeContext.payload.length > 0
    ? Buffer.from(resumeContext.payload).toString("utf8")
    : "";
  const resumeReason = resumeContext?.resumeReason ?? "";

  const renderedSystem = systemPrompt;
  // `binding_id` is the raw `ExecuteRequest.dispatch_id`. Validators sign
  // `domain ‖ binding_id ‖ canonical(content)`; the agent relays this id to
  // each validator so the signature it returns binds to this exact dispatch,
  // letting the sign-off gate re-derive identical bytes. Empty when the
  // dispatch carried no `dispatch_id` (an ungatable/usage-error case).
  const renderedUser =
    userPrompt +
    "\n\n---\n" +
    `callback_token: ${callbackToken}\n` +
    `binding_id: ${dispatchId ?? ""}\n` +
    `resume_payload: ${resumePayload}\n` +
    `resume_reason: ${resumeReason}\n` +
    "---\n";

  // Per-dispatch internal MCP server. The shared / global server on `callback`
  // (passed in via RunArgs) was found to mishandle multi-spawn lifecycles —
  // a second dispatch's CLI couldn't `initialize` the MCP after the first
  // dispatch ran, and the rimsky-callback tools silently disappeared from
  // the agent's tool surface. Starting a fresh server per dispatch mirrors
  // skillprompting/brain (mcp-topic-server.ts::startTopicMcpServer), which
  // is the production reference for this spawn-claude → MCP-HTTP loop.
  const dispatchMcp = await startInternalMcpServer({
    host: "127.0.0.1",
    port: 0,
    logger,
  });
  let dispatchMcpClosed = false;
  const closeDispatchMcp = async (): Promise<void> => {
    if (dispatchMcpClosed) return;
    dispatchMcpClosed = true;
    try {
      await dispatchMcp.close();
    } catch (err) {
      logger.warn({ runId, error: String(err) }, "dispatch MCP close failed");
    }
  };

  // Per-dispatch teardowns for any catalog transport stood up at the
  // `hostServers` build site (module / http-loopback loopback listeners).
  // Wired into the dispatch-end cleanup so a long-lived loopback HTTP
  // listener never leaks past its dispatch.
  const catalogTeardowns: Array<() => Promise<void>> = [];
  let catalogToreDown = false;
  const tearDownCatalogServers = async (): Promise<void> => {
    if (catalogToreDown) return;
    catalogToreDown = true;
    for (const td of catalogTeardowns) {
      try {
        await td();
      } catch (err) {
        logger.warn(
          { runId, error: String(err) },
          "catalog server teardown failed",
        );
      }
    }
  };
  // Effective callback handle for THIS dispatch. The passed-in `callback`
  // parameter is preserved on the RunArgs interface for back-compat but its
  // url/registry are not used by this run.
  const effectiveCallback = dispatchMcp;

  // Lazily compile the attributes schema if one is provided; ajv throws on
  // an invalid schema shape which we surface as an errored outcome before
  // we spawn. Per spec §5.7.1 rimsky also re-validates at commit, but
  // catching obviously broken schemas pre-spawn fails fast.
  const ajv = new Ajv({ allErrors: true, strict: false });
  let validateAttributes: ((v: unknown) => boolean) | null = null;
  const schemaErrors: string[] = [];
  if (
    attributesSchema &&
    typeof attributesSchema === "object" &&
    Object.keys(attributesSchema as object).length > 0
  ) {
    try {
      validateAttributes = ajv.compile(attributesSchema as object);
    } catch (e) {
      schemaErrors.push(`invalid attributes_schema: ${String(e)}`);
    }
  }

  if (schemaErrors.length > 0) {
    return {
      kind: "errored",
      errorClass: "agent/attribute_invalid",
      payload: { errors: schemaErrors },
    };
  }

  let resolved = false;
  let resolveOutcome!: (o: AgentOutcome) => void;
  const outcomePromise = new Promise<AgentOutcome>((r) => {
    resolveOutcome = r;
  });
  const safeResolve = (o: AgentOutcome): void => {
    if (resolved) return;
    resolved = true;
    resolveOutcome(o);
  };

  let handleRef: CliHandle | null = null;
  let teardownInProgress = false;
  const teardownResolveRef = { fn: (): void => {} };
  const teardownCli = async (): Promise<void> => {
    const h = handleRef;
    if (!h) {
      teardownResolveRef.fn();
      return;
    }
    teardownInProgress = true;
    h.sendSigterm();
    let graceTimer: NodeJS.Timeout | null = null;
    await Promise.race([
      h.waitExit(),
      new Promise<void>((r) => {
        graceTimer = setTimeout(r, 5000);
      }),
    ]);
    if (graceTimer) clearTimeout(graceTimer);
    h.sendSigkill();
    let killTimer: NodeJS.Timeout | null = null;
    let killTimedOut = false;
    await Promise.race([
      h.waitExit(),
      new Promise<void>((r) => {
        killTimer = setTimeout(() => {
          killTimedOut = true;
          r();
        }, 5000);
      }),
    ]);
    if (killTimer) clearTimeout(killTimer);
    if (killTimedOut) {
      logger.warn({ runId }, "teardownCli: child did not exit within 5s of SIGKILL");
    }
    teardownResolveRef.fn();
  };

  // Wire the writeback function the `attributes_set` MCP tool calls.
  // Path segment is `run_id` (= dispatch_id) per the 2026-05-20 per-run
  // keying refactor; the URL builder accepts the run id directly.
  const post = postAttributes ?? defaultPostAttributes;
  const writebackUrl = callbackUrl
    ? buildAttributesWritebackUrl(callbackUrl, runId)
    : "";
  // Run-local mirror of the incremental `attributes_set` writeback state the
  // supervisor accumulates. Each accepted (non-error POST) `delta` is shallow-
  // merged here, last-write-wins — mirroring the supervisor's own merge — so
  // that at sign-off-gate time the executor can reconstruct the EFFECTIVE bound
  // bag the supervisor will commit. On the incremental path `report_complete`
  // omits the terminal-final `attributes_delta`, so this accumulator is the only
  // place the run's real bound output lives; binding the gate to it (rather than
  // to the absent terminal delta) is the load-bearing correctness property for
  // S-executors-signoff-binds-real-output.
  const accumulatedWriteback: Record<string, unknown> = {};
  const onAttributesSet = async (
    delta: Record<string, unknown>,
  ): Promise<{ status: number }> => {
    if (!writebackUrl) {
      logger.warn({ runId }, "attributes_set called but no callback_url; dropping");
      return { status: 503 };
    }
    try {
      const result = await post(writebackUrl, { delta }, cancelToken);
      // Accumulate only on a supervisor-accepted writeback (2xx). A rejected
      // POST never reaches the supervisor's committed state, so it must not
      // enter the bag the gate binds — otherwise the gate would bind output the
      // supervisor never persisted.
      if (result.status >= 200 && result.status < 300) {
        Object.assign(accumulatedWriteback, delta);
      }
      return result;
    } catch (e) {
      logger.error(
        { runId, error: String(e) },
        "attributes writeback POST failed",
      );
      return { status: 502 };
    }
  };

  // J8: track corrective `report_complete` retries. After more than
  // maxSchemaCorrections (default 3) consecutive validation failures,
  // the run terminates with a StreamClose `Error{error_class:
  // "agent/schema_violation"}` outcome on the wire.
  const maxSchemaCorrections =
    typeof cliConfig?.maxSchemaCorrections === "number" && cliConfig.maxSchemaCorrections >= 0
      ? cliConfig.maxSchemaCorrections
      : 3;
  let schemaCorrectionFailures = 0;

  // rejectWithCorrection bumps the corrective-retry counter. When the
  // counter exceeds the cap, it schedules teardown with an `errored`
  // outcome AND returns "accepted" so the agent's tool call resolves
  // (the run is committed; the agent sees the success but the
  // supervisor receives a StreamClose Error outcome). Otherwise it
  // returns "rejected" with a corrective message — the agent can
  // re-call report_complete with a fixed delta.
  const rejectWithCorrection = (
    detail: string,
    scheduleTeardown: (td: () => Promise<void>) => void,
  ): { status: "accepted" } | { status: "rejected"; errors: Record<string, string[]> } => {
    schemaCorrectionFailures++;
    if (schemaCorrectionFailures > maxSchemaCorrections) {
      logger.warn(
        { runId, failures: schemaCorrectionFailures, max: maxSchemaCorrections },
        "report_complete: schema corrections exhausted; committing errored",
      );
      scheduleTeardown(async () => {
        await teardownCli();
        safeResolve({
          kind: "errored",
          errorClass: "agent/schema_violation",
          payload: {
            attempts: schemaCorrectionFailures,
            max: maxSchemaCorrections,
            last_error: detail,
          },
        });
      });
      return { status: "accepted" };
    }
    return {
      status: "rejected",
      errors: {
        attributes_delta: [
          `${detail} (correction ${schemaCorrectionFailures}/${maxSchemaCorrections})`,
        ],
      },
    };
  };

  // Sign-off gate correction loop, mirroring `rejectWithCorrection`. The
  // sign-off gate runs in `onComplete` AFTER schema validation passes: get
  // the shape right (rejectWithCorrection), then get it signed (rejectSignoff).
  // Each layer carries its own retry budget. On exhausting
  // maxSignoffAttempts the run commits a terminal `errored` outcome with
  // error_class "agent/signoff_unobtained" (parallel to the schema layer's
  // "agent/schema_violation") and returns "accepted" so the agent's tool
  // call resolves while the supervisor receives the StreamClose Error.
  const maxSignoffAttempts =
    typeof cliConfig?.maxSignoffAttempts === "number" && cliConfig.maxSignoffAttempts >= 0
      ? cliConfig.maxSignoffAttempts
      : 3;
  let signoffFailures = 0;
  const rejectSignoff = (
    detail: string,
    scheduleTeardown: (td: () => Promise<void>) => void,
  ): { status: "accepted" } | { status: "rejected"; errors: Record<string, string[]> } => {
    signoffFailures++;
    if (signoffFailures > maxSignoffAttempts) {
      logger.warn(
        { runId, failures: signoffFailures, max: maxSignoffAttempts },
        "report_complete: sign-offs unobtained; committing errored",
      );
      scheduleTeardown(async () => {
        await teardownCli();
        safeResolve({
          kind: "errored",
          errorClass: "agent/signoff_unobtained",
          payload: {
            attempts: signoffFailures,
            max: maxSignoffAttempts,
            last_error: detail,
          },
        });
      });
      return { status: "accepted" };
    }
    return {
      status: "rejected",
      errors: {
        signoffs: [`${detail} (signoff ${signoffFailures}/${maxSignoffAttempts})`],
      },
    };
  };

  // Per-dispatch named-event sink. The `emit_named_event` MCP tool
  // appends one `{name, payload}` per accepted emission; the buffer rides
  // the async-callback body's `events[]` array when the run resolves. The
  // declared-events list is the executor's resolved `declared_events`
  // (RIMSKY_EXECUTOR_DECLARED_EVENTS) — the tool's self-consistency check.
  const emittedEvents: NamedEventEmission[] = [];
  const declaredEvents = resolveDeclaredEvents();

  effectiveCallback.registry.register(callbackToken, {
    runId,
    attributesAtSpawn: attributes,
    cancelToken,
    nodeId,
    callbackUrl,
    declaredEvents,
    emittedEvents,
    emitNamedEvent: (name, payload) => {
      emittedEvents.push({ name, payload });
    },
    onComplete: async (
      attributesDelta,
      changed,
      changeSummary,
      // `signoffs` is the agent-presented Ed25519 signature bag. The sign-off
      // gate below verifies it against the dispatch-time `required_signoffs`
      // before the dispatch can resolve to terminal success.
      signoffs,
      scheduleTeardown,
    ) => {
      // Validate any executor-supplied terminal-final delta before
      // accepting. Per spec §12.2, the delta is optional (executors using
      // incremental writeback omit it).
      if (attributesDelta !== null) {
        if (typeof attributesDelta !== "object" || Array.isArray(attributesDelta)) {
          return rejectWithCorrection(
            "must be an object",
            scheduleTeardown,
          );
        }
        try {
          JSON.stringify(attributesDelta);
        } catch (e) {
          return rejectWithCorrection(
            `unserializable_attributes_delta: ${String(e)}`,
            scheduleTeardown,
          );
        }
        if (validateAttributes) {
          // The delta merged on top of the dispatch-time attributes is
          // what the supervisor will validate authoritatively; we do a
          // best-effort local check on the same merged shape.
          const merged = { ...attributes, ...attributesDelta };
          if (!validateAttributes(merged)) {
            const errs =
              (validateAttributes as unknown as { errors?: unknown[] }).errors ?? [];
            return rejectWithCorrection(
              errs.map((e) => JSON.stringify(e)).join("; ") || "validation failed",
              scheduleTeardown,
            );
          }
        }
      }
      // Validation passed — reset the corrective-retry counter so a
      // future delta replacement starts fresh.
      schemaCorrectionFailures = 0;

      // The EFFECTIVE bound bag = the accumulated incremental `attributes_set`
      // writebacks with the terminal-final `report_complete` delta layered on
      // top (last-write-wins). This is exactly the bag the supervisor will
      // commit: on the terminal-delta path the accumulator is empty so the merge
      // is identity (`effectiveBag === attributesDelta`); on the incremental path
      // `attributesDelta` is null so the bag is the accumulated writeback. The
      // sign-off gate binds — and the run commits — THIS value, so an unsigned or
      // stale-signed incremental run cannot pass the gate over the absent
      // terminal delta (S-executors-signoff-binds-real-output).
      const effectiveBag: Record<string, unknown> = {
        ...accumulatedWriteback,
        ...(attributesDelta ?? {}),
      };

      // Sign-off gate (the second sequential layer, after schema validation).
      // `required` and `dispatchId` come from DISPATCH-TIME inputs
      // (`cliConfig.requiredSignoffs` resolved at spawn, and the raw
      // `dispatch_id` plumbed onto the run options) — NEVER from
      // `attributesDelta`, the accumulated writeback, or the effective bag. This
      // is what makes the gate tamper-proof: a gated agent cannot weaken or edit
      // its own gate by emitting a `cli.required_signoffs` override inside its
      // output, and a signature can only bind to the one real dispatch
      // (anti-replay). Only the VALUE the gate binds (the effective bag) flows
      // from agent-supplied output; `required`/`dispatchId` do not.
      const required = cliConfig?.requiredSignoffs ?? [];
      if (required.length > 0) {
        if (!dispatchId || dispatchId.length === 0) {
          // A configured gate with no dispatch_id cannot be bound or verified
          // (the binding id is empty, so no honest signature can re-derive the
          // same bytes). Treat it as a configuration/usage error rather than a
          // silently-ungated run that would let unsigned output through.
          logger.warn(
            { runId },
            "report_complete: sign-off gate configured but dispatch_id empty; committing errored",
          );
          scheduleTeardown(async () => {
            await teardownCli();
            safeResolve({
              kind: "errored",
              errorClass: "agent/signoff_unobtained",
              payload: {
                error: "dispatch_id required for sign-off gate but was empty",
              },
            });
          });
          return { status: "accepted" };
        }
        const res = verifyRequiredSignoffs(
          required,
          effectiveBag,
          dispatchId,
          signoffs ?? [],
        );
        if (!res.ok) {
          const detail = res.unmet
            .map((u) => `${u.path}:${u.reason}`)
            .join(", ");
          return rejectSignoff(`unmet sign-offs: ${detail}`, scheduleTeardown);
        }
        // Gate satisfied — reset the sign-off retry counter so a future
        // correction round (e.g. after a schema re-edit) starts fresh.
        signoffFailures = 0;
      }

      // Commit the EFFECTIVE bound bag, not the raw terminal-final delta. The
      // gate verified its signature over this exact value, so the committed
      // output is the one bound. On the incremental path the bag is the
      // accumulated writeback (which the agent omitted from `report_complete`);
      // re-sending it in the terminal delta is idempotent against the
      // supervisor's last-write-wins merge of the already-POSTed writebacks, so
      // the supervisor's committed state equals the bound value. An empty bag
      // (no writeback, no terminal delta) collapses back to `null` to preserve
      // the existing "no delta" wire shape.
      const committedDelta =
        Object.keys(effectiveBag).length > 0 ? effectiveBag : null;
      scheduleTeardown(async () => {
        await teardownCli();
        safeResolve({
          kind: "complete",
          attributesDelta: committedDelta,
          changed,
          changeSummary,
        });
      });
      return { status: "accepted" };
    },
    onBlocked: async (reason, context, scheduleTeardown) => {
      scheduleTeardown(async () => {
        await teardownCli();
        safeResolve({ kind: "blocked", reason, context });
      });
    },
    onError: async (errorClass, payload, scheduleTeardown) => {
      scheduleTeardown(async () => {
        await teardownCli();
        safeResolve({ kind: "errored", errorClass, payload });
      });
    },
    onPark: async (reason, reasonNote, resumeAtISO, scheduleTeardown) => {
      // Agent invoked the MCP report_park tool. Resolve the per-run
      // outcome promise with the park_requested shape; the server-side
      // gRPC bridge translates the typed reason / reasonNote into the
      // Park terminal (PARK_REASON_<UPPER> + reason_note). Per
      // 2026-05-14 Piece 2 the rate-limit path uses the same outcome
      // shape; only the reason discriminator differs.
      let parsedResumeAt: Date | null = null;
      if (resumeAtISO !== null && resumeAtISO.length > 0) {
        const d = new Date(resumeAtISO);
        if (!Number.isNaN(d.getTime())) {
          parsedResumeAt = d;
        }
      }
      scheduleTeardown(async () => {
        await teardownCli();
        safeResolve({
          kind: "park_requested",
          reason,
          reasonNote: reasonNote ?? "",
          payload: new Uint8Array(),
          resumeAt: parsedResumeAt,
          sessionToken: runId,
        });
      });
    },
    onAttributesSet,
  });

  // Host-wired MCP servers (`cli.mcp_servers`). Each declared server is
  // appended to the spawned CLI's `--mcp-config` (the per-spawn `tools`
  // list) so the agent can actually dial it — a URL only mentioned in a
  // prompt is unreachable; the Claude CLI only speaks MCP to servers it was
  // configured with via `--mcp-config`. The same list is applied on both
  // resume paths so a resumed dispatch can still reach the servers (the CLI
  // does not carry `--mcp-config` across `--resume`).
  //
  // This is the SINGLE resolution site (per the plan's grounding): the
  // `hostServers` list is built once here and spread into the one
  // `cliRunner.spawn` and the two `cliRunner.resume` call sites below, so
  // resolving `{ ref: }` catalog references and standing up module /
  // http-loopback transports here covers spawn AND every resume path.
  //
  // Each entry is either an inline `{ name, url }` server or a catalog
  // reference `{ ref: <name> }`. Inline servers are rejected when
  // `allow_inline` is false (catalog is the authoritative source); refs
  // resolve against `mcpCatalog`, with module / http-loopback transports
  // stood up on a per-dispatch loopback HTTP listener whose teardown is
  // registered for dispatch-end cleanup.
  let hostServers: CliToolConfig[];
  let hostAllowed: string[];
  try {
    const resolved = await resolveHostServers(
      cliConfig?.mcpServers ?? [],
      mcpCatalog ?? {},
      mcpAllowInline,
      catalogTeardowns,
      logger,
    );
    // Resolve `${env:VAR}` references in host-server connection headers at
    // this single spawn-boundary site (S-executors-validator-header-secret-refs).
    // `resolveHeaderEnvRefs` returns a FRESH header map, so the persisted/traced
    // `cliConfig.mcpServers` form (read from the parsed node attributes, never
    // touched here) keeps its `${env:...}` reference and the resolved secret
    // lives only in this transient `--mcp-config`-bound `tools` list. Covers
    // both inline and catalog `http` transports uniformly; non-http (stdio)
    // leaves carry no headers and pass through unchanged.
    hostServers = resolved.tools.map((tool) =>
      tool.kind === "mcp-http"
        ? { ...tool, headers: resolveHeaderEnvRefs(tool.headers) }
        : tool,
    );
    hostAllowed = resolved.allowedTools;
  } catch (e) {
    await tearDownCatalogServers();
    effectiveCallback.registry.release(callbackToken);
    await closeDispatchMcp();
    if (e instanceof CliConfigError) {
      return {
        kind: "errored",
        errorClass: e.errorClass,
        payload: { reason: e.message },
      };
    }
    return {
      kind: "errored",
      errorClass: "agent/attribute_invalid",
      payload: { reason: String(e) },
    };
  }
  // Union of per-template `allowedTools` and the host-server auto-allows.
  // Pass `undefined` when both are empty so spawn/resume preserve the
  // current behavior (the required callback tools are always added by
  // `buildAllowedTools` regardless).
  const templateAllowed = cliConfig?.allowedTools ?? [];
  const allowedToolsUnion =
    templateAllowed.length > 0 || hostAllowed.length > 0
      ? [...templateAllowed, ...hostAllowed]
      : undefined;

  let handle: CliHandle;
  try {
    // J10: When resumeContext.sessionToken is set we resume the same
    // CLI session so the agent's prior context (tool calls, memory,
    // partial work) is preserved across the park boundary. Otherwise
    // this is a fresh dispatch.
    if (
      resumeContext?.sessionToken &&
      resumeContext.sessionToken.length > 0 &&
      cliRunner.resume !== undefined
    ) {
      logger.info(
        { runId, session_token: resumeContext.sessionToken, resume_reason: resumeContext.resumeReason ?? "" },
        "cli.resume_after_park",
      );
      handle = await cliRunner.resume({
        sessionId: resumeContext.sessionToken,
        prompt: renderedUser,
        tools: [
          { kind: "mcp-http", name: "rimsky-callback", url: effectiveCallback.url },
          ...hostServers,
        ],
        // Resume must re-emit the host-server allowlist too: `--allowedTools`
        // is process-local invocation config, not session state, so a resumed
        // dispatch that omits it cannot call the host validators' tools.
        allowedTools: allowedToolsUnion,
        env: {
          RIMSKY_CALLBACK_URL: effectiveCallback.url,
          RIMSKY_CALLBACK_TOKEN: callbackToken,
        },
        cwd,
      });
    } else {
      handle = await cliRunner.spawn({
        model,
        systemPrompt: renderedSystem,
        userPrompt: renderedUser,
        tools: [
          { kind: "mcp-http", name: "rimsky-callback", url: effectiveCallback.url },
          ...hostServers,
        ],
        env: {
          RIMSKY_CALLBACK_URL: effectiveCallback.url,
          RIMSKY_CALLBACK_TOKEN: callbackToken,
        },
        cwd,
        // runId is the rimsky-side UUID for this dispatch. Reusing it as
        // the CLI's session-id gives stable trace correlation AND lets us
        // resume the same session on the post-exit retry path below.
        sessionId: runId,
        bare: cliConfig?.bare,
        permissionMode: cliConfig?.permissionMode,
        // Union of per-template allowed tools and the host-server
        // auto-allows; `undefined` when both are empty (current behavior).
        allowedTools: allowedToolsUnion,
        disallowedTools: cliConfig?.disallowedTools,
        addDirs: cliConfig?.addDirs,
        maxBudgetUsd: cliConfig?.maxBudgetUsd,
      });
    }
    logger.info(
      {
        runId,
        pid: handle.pid,
        model,
        cwd,
        bare: cliConfig?.bare ?? false,
        permission_mode: cliConfig?.permissionMode ?? "bypassPermissions",
        mcp_url: effectiveCallback.url,
      },
      "cli.spawned",
    );
  } catch (e) {
    effectiveCallback.registry.release(callbackToken);
    void closeDispatchMcp();
    // Spawn failed after catalog transports were stood up; tear them down so
    // a loopback listener doesn't leak past the aborted dispatch.
    await tearDownCatalogServers();
    return {
      kind: "errored",
      errorClass: "agent/cli_spawn_failed",
      payload: { error: String(e) },
    };
  }
  handleRef = handle;

  let lastStdoutAt = Date.now();
  // Bounded stderr buffer for J9 rate-limit detection. We only care
  // about the most recent ~16 KB; older bytes are dropped via shift.
  const stderrCap = 16 * 1024;
  let stderrBuf = "";
  handle.onStdout((chunk) => {
    lastStdoutAt = Date.now();
    logger.info({ runId, chunk: chunk.slice(0, 2000) }, "cli.stdout");
  });
  handle.onStderr((chunk) => {
    logger.warn({ runId, chunk: chunk.slice(0, 2000) }, "cli.stderr");
    stderrBuf += chunk;
    if (stderrBuf.length > stderrCap) {
      stderrBuf = stderrBuf.slice(stderrBuf.length - stderrCap);
    }
  });

  let teardownResolve!: () => void;
  const teardownDone = new Promise<void>((r) => {
    teardownResolve = r;
  });
  teardownResolveRef.fn = teardownResolve;

  let silenceStopped = false;
  const silenceLoop = (async (): Promise<void> => {
    const pollMs = 100;
    while (!resolved && !silenceStopped) {
      await new Promise((r) => setTimeout(r, pollMs));
      if (resolved || silenceStopped || teardownInProgress) return;
      const now = Date.now();
      if (now - lastStdoutAt > silenceTimeoutMs) {
        teardownInProgress = true;
        try {
          handle.sendSigterm();
          let graceTimer: NodeJS.Timeout | null = null;
          await Promise.race([
            handle.waitExit(),
            new Promise<void>((r) => {
              graceTimer = setTimeout(r, 500);
            }),
          ]);
          if (graceTimer) clearTimeout(graceTimer);
          handle.sendSigkill();
          let killTimer: NodeJS.Timeout | null = null;
          await Promise.race([
            handle.waitExit(),
            new Promise<void>((r) => {
              killTimer = setTimeout(r, 5000);
            }),
          ]);
          if (killTimer) clearTimeout(killTimer);
        } catch {
          // subprocess may already be gone.
        }
        teardownResolve();
        safeResolve({
          kind: "errored",
          errorClass: "agent/timeout",
          payload: { silence_duration_ms: now - lastStdoutAt },
        });
        return;
      }
    }
  })();

  const spawnedAt = Date.now();
  void (async () => {
    const { exitCode, signal } = await handle.waitExit();
    logger.info(
      {
        runId,
        pid: handle.pid,
        exit_code: exitCode,
        signal,
        duration_ms: Date.now() - spawnedAt,
      },
      "cli.exited",
    );
    let raceTimer: NodeJS.Timeout | null = null;
    await Promise.race([
      teardownDone,
      new Promise<void>((r) => {
        raceTimer = setTimeout(r, 2000);
      }),
    ]);
    if (raceTimer) clearTimeout(raceTimer);
    if (teardownInProgress) return;
    if (resolved) return; // a terminal MCP callback fired between exit and now

    // J9: rate-limit auto-park. When the CLI dies non-zero AND its
    // stderr carries a rate-limit signal AND attributes.cli.handle_rate_limits
    // is enabled (default true), emit `park_requested` instead of
    // bouncing through the recovery path. The supervisor receives the
    // `Park` terminal and parks the node until the reset window (or
    // external invalidate) fires.
    const handleRateLimits = cliConfig?.handleRateLimits !== false;
    if (
      exitCode !== 0 &&
      exitCode !== null &&
      stderrBuf.length > 0
    ) {
      const signalRL = detectRateLimit(stderrBuf, new Date());
      // When the operator has opted OUT of auto-park (handle_rate_limits=false),
      // a detected rate-limit surfaces as the declared `agent/rate_limited`
      // Error class instead of a Park — so a subscriber/policy keyed on that
      // class actually fires (S-executors-claude-agent-error-classes). The
      // default (handle_rate_limits=true) auto-park behavior below is left
      // intact: only the false branch diverts to an Error.
      if (signalRL.detected && !handleRateLimits) {
        logger.warn(
          {
            runId,
            exit_code: exitCode,
            signal,
          },
          "cli.rate_limit_detected; handle_rate_limits=false → emitting agent/rate_limited Error",
        );
        safeResolve({
          kind: "errored",
          errorClass: "agent/rate_limited",
          payload: {
            exitCode,
            signal,
            resume_at: signalRL.resumeAt?.toISOString() ?? null,
          },
        });
        return;
      }
      if (signalRL.detected && handleRateLimits) {
        logger.warn(
          {
            runId,
            exit_code: exitCode,
            signal,
            resume_at: signalRL.resumeAt?.toISOString() ?? null,
          },
          "cli.rate_limit_detected; emitting park_requested",
        );
        safeResolve({
          kind: "park_requested",
          // Rate-limit-aware waits classify as PARK_REASON_SNOOZE
          // (the closed two-value set's deadline-based reason) — the
          // CLI surfaces a wall-clock resume_at, which the supervisor
          // wakes via SweepParkedNodes. Per spec
          // .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
          // §ParkReason collapse. `reasonNote` preserves the prior
          // free-form `reason` text so operators / dashboards still
          // see "claude rate-limit detected; resume at <ts>".
          reason: "snooze",
          reasonNote:
            signalRL.reason !== ""
              ? signalRL.reason
              : "claude cli rate-limit detected; resume_at=" +
                (signalRL.resumeAt?.toISOString() ?? "indefinite"),
          payload: new Uint8Array(),
          resumeAt: signalRL.resumeAt,
          // The CLI session id is the rimsky run id; resume passes
          // it back via ResumeContext.session_token for `--resume`.
          sessionToken: runId,
        });
        return;
      }
    }

    // Recovery path: subprocess exited cleanly (code 0) but never called
    // mcp__rimsky-callback__report_complete. The orchestrator pattern's
    // long Task-subagent chains seem prone to this — the agent loses the
    // imperative for the final tool call after several context-heavy
    // turns. We resume the same session by id and inject a one-shot
    // reminder prompt; the agent's full prior context (including the
    // work it did) is intact, and the callback MCP server is still up
    // (per-dispatch lifecycle bound to runAgent), so calling
    // report_complete from the resumed session lands cleanly.
    if (
      exitCode === 0 &&
      signal === null &&
      cliRunner.resume !== undefined
    ) {
      const reminderPrompt =
        "You exited without calling mcp__rimsky-callback__report_complete. " +
        "Review what you accomplished in this session and call the appropriate " +
        "callback now: report_complete (with changed:true if you applied edits, " +
        "changed:false if you found nothing to change), report_blocked (if " +
        "something prevented you from finishing), or report_error (if you hit " +
        "an unexpected failure). This is REQUIRED — without it rimsky treats " +
        "the dispatch as failed and discards your work.";
      logger.warn(
        { runId, exit_code: exitCode, duration_ms: Date.now() - spawnedAt },
        "cli.clean_exit_no_report; attempting resume",
      );
      try {
        const retryHandle = await cliRunner.resume({
          sessionId: runId,
          prompt: reminderPrompt,
          tools: [
            { kind: "mcp-http", name: "rimsky-callback", url: effectiveCallback.url },
            ...hostServers,
          ],
          // Re-emit the host-server allowlist on the recovery resume too.
          allowedTools: allowedToolsUnion,
          env: {
            RIMSKY_CALLBACK_URL: effectiveCallback.url,
            RIMSKY_CALLBACK_TOKEN: callbackToken,
          },
          cwd,
        });
        handleRef = retryHandle;
        retryHandle.onStdout((chunk) => {
          lastStdoutAt = Date.now();
          logger.info(
            { runId, retry: true, chunk: chunk.slice(0, 2000) },
            "cli.stdout",
          );
        });
        retryHandle.onStderr((chunk) => {
          logger.warn(
            { runId, retry: true, chunk: chunk.slice(0, 2000) },
            "cli.stderr",
          );
        });
        const retryStartedAt = Date.now();
        const retryResult = await retryHandle.waitExit();
        logger.info(
          {
            runId,
            retry: true,
            pid: retryHandle.pid,
            exit_code: retryResult.exitCode,
            signal: retryResult.signal,
            duration_ms: Date.now() - retryStartedAt,
          },
          "cli.exited",
        );
        // #11: the resumed CLI's terminal report (`report_complete`)
        // drives `onComplete`, which DEFERS teardown via setTimeout(0);
        // teardown then sends SIGTERM (which is what just resolved
        // `retryHandle.waitExit()` above) and only AFTERWARD runs the
        // terminal `safeResolve(complete)`. So at this point the resumed
        // report may have landed and accepted, yet `resolved` is not yet
        // true because the deferred terminal resolution is still queued.
        // Without a grace window the retry path races ahead and
        // mis-classifies a completed dispatch as
        // `agent/subprocess_exit/before_complete`. Mirror the main-exit
        // path's `teardownDone`-vs-timer race so the in-flight terminal
        // settles before we conclude failure. Property protected: a
        // report_complete that landed on resume always wins over the
        // before_complete fallback.
        if (!resolved) {
          let graceTimer: NodeJS.Timeout | null = null;
          await Promise.race([
            teardownDone,
            new Promise<void>((r) => {
              graceTimer = setTimeout(r, 2000);
            }),
          ]);
          if (graceTimer) clearTimeout(graceTimer);
          // Yield once more so the terminal `safeResolve` sequenced right
          // after `teardownDone` inside the deferred teardown runs before
          // we re-check `resolved`.
          await new Promise<void>((r) => setImmediate(r));
        }
        if (resolved) return; // retry's MCP callback fired — outcome already set
        safeResolve({
          kind: "errored",
          errorClass: "agent/subprocess_exit/before_complete",
          payload: {
            exitCode: retryResult.exitCode,
            signal: retryResult.signal,
            retry_attempted: true,
          },
        });
        return;
      } catch (err) {
        logger.warn(
          { runId, error: String(err) },
          "cli.resume_failed",
        );
        safeResolve({
          kind: "errored",
          errorClass: "agent/subprocess_exit/before_complete",
          payload: { exitCode, signal, retry_failed: String(err) },
        });
        return;
      }
    }

    // Classify the subprocess failure to the precise declared leaf
    // (agent/context_exceeded, agent/refused, agent/tool_use_failed/<tool>)
    // when the stderr carries a recognized signature, so a subscriber/policy
    // keyed on one of those classes fires (S-executors-claude-agent-error-
    // classes). An unrecognized non-zero exit keeps the generic
    // `agent/subprocess_exit/before_complete` leaf (unchanged behavior).
    const classified = classifyAgentError(stderrBuf, exitCode);
    safeResolve({
      kind: "errored",
      errorClass: classified?.errorClass ?? "agent/subprocess_exit/before_complete",
      payload: { exitCode, signal },
    });
  })();

  try {
    const outcome = await outcomePromise;
    // Stamp the per-dispatch named-event buffer onto the resolved outcome
    // so `outcomeToCallbackBody` can ride the events on the async-callback
    // body's `events[]` array (the gRPC stream already closed at dispatch).
    // Buffer is captured before the registry entry is released below.
    if (emittedEvents.length > 0) {
      outcome.emittedEvents = emittedEvents;
    }
    return outcome;
  } finally {
    silenceStopped = true;
    teardownResolve();
    await silenceLoop.catch(() => {});
    effectiveCallback.registry.release(callbackToken);
    void closeDispatchMcp();
    // Tear down any per-dispatch catalog transport listener (module /
    // http-loopback). Awaited so a fast follow-on dispatch doesn't race a
    // still-listening loopback server on a leaked port.
    await tearDownCatalogServers();
  }
}

/**
 * Resolves a node's `cli.mcp_servers` list into the concrete
 * `CliToolConfig` leaves to wire into `--mcp-config`, plus the host-server
 * auto-allow entries for `--allowedTools`. S-executors-mcp-catalog-transports.
 *
 * Per entry:
 *   - `{ ref: <name> }` — looked up in `catalog`. Unknown ref → config
 *     error (the host named a server that does not exist). The catalog
 *     entry's transport determines the emitted leaf: http / stdio resolve
 *     directly; module / http-loopback stand up a per-dispatch loopback HTTP
 *     listener whose teardown is pushed onto `teardowns`.
 *   - inline `{ name, url }` — permitted only when `allowInline` is true;
 *     rejected with a config error citing `allow_inline` otherwise.
 *
 * Auto-allow rule (unchanged from the prior inline-only path): with no
 * explicit per-server `allowedTools` the bare `mcp__<name>` server-prefix
 * entry allows ALL of that server's tools; an explicit list narrows it to
 * fully-qualified names.
 *
 * Throws `CliConfigError` on any unresolvable/forbidden entry so the caller
 * surfaces a fail-loud `agent/attribute_invalid` terminal rather than
 * silently dropping a server (which could unwire a validator the gate
 * depends on). Any listeners already stood up before the throw are still
 * registered in `teardowns` for the caller to clean up.
 */
async function resolveHostServers(
  servers: HostMcpServerInput[],
  catalog: McpCatalog,
  allowInline: boolean | undefined,
  teardowns: Array<() => Promise<void>>,
  logger: Logger,
): Promise<{ tools: CliToolConfig[]; allowedTools: string[] }> {
  const tools: CliToolConfig[] = [];
  const allowedTools: string[] = [];
  for (const s of servers) {
    if ("ref" in s) {
      const entry = catalog[s.ref];
      if (entry === undefined) {
        throw new CliConfigError(
          `cli.mcp_servers references unknown catalog server "${s.ref}" ` +
            `(no such entry in the startup MCP catalog)`,
        );
      }
      const resolved = await resolveCatalogServer(s.ref, entry, logger);
      teardowns.push(resolved.teardown);
      tools.push(resolved.tool);
      allowedTools.push(...autoAllow(s.ref, resolved.allowedTools));
      continue;
    }
    // Inline server: rejected only when the policy is EXPLICITLY off
    // (`allow_inline === false`). An unset policy (`undefined`) is the
    // legacy no-catalog deployment where inline is the only mechanism, so
    // it stays permissive. A real deployment always sets the policy via
    // `main.ts` (`parsePolicy` → default false), so an operator-configured
    // catalog deployment rejects inline by default, per the spec.
    if (allowInline === false) {
      throw new CliConfigError(
        `cli.mcp_servers declares an inline server "${s.name}" but ` +
          `allow_inline is false — reference a catalog server via { ref: <name> } ` +
          `or enable RIMSKY_EXECUTOR_MCP_ALLOW_INLINE`,
      );
    }
    tools.push({
      kind: "mcp-http",
      name: s.name,
      url: s.url,
      headers: s.headers,
    });
    allowedTools.push(...autoAllow(s.name, s.allowedTools));
  }
  return { tools, allowedTools };
}

/** Auto-allow a host server's tools: explicit per-server list → fully-
 *  qualified names; absent → the bare server-prefix entry (all tools). */
function autoAllow(name: string, allowedTools?: string[]): string[] {
  return allowedTools && allowedTools.length > 0
    ? allowedTools.map((t) => `mcp__${name}__${t}`)
    : [`mcp__${name}`];
}

type CwdResolution =
  | { kind: "ok"; cwd: string | undefined }
  | { kind: "error"; message: string };

/**
 * Resolve the spawn cwd from store handles + attribute hints.
 *
 * Precedence:
 *   1. `cwdFromStore` — look up `stores[<name>].handle.address`. The
 *      filesystem store sets this to an absolute path. Must be a string,
 *      and must point to an existing directory at spawn time.
 *   2. `cwdOverride` — raw path from `attributes.cwd`. Validated the same
 *      way (must exist, must be a directory).
 *   3. Neither set → undefined; the subprocess inherits the executor
 *      process's cwd.
 *
 * Validation deliberately stat-checks the path: a typo'd selector or a
 * volume-mount mismatch between the store-service and the executor pod
 * would otherwise fail opaquely deep inside `claude` after the spawn.
 *
 * Exported for tests; not part of the agent-contract surface.
 */
export function resolveCwd(args: {
  stores: Record<string, unknown>;
  cwdFromStore: string | undefined;
  cwdOverride: string | undefined;
}): CwdResolution {
  const { stores, cwdFromStore, cwdOverride } = args;
  if (cwdFromStore && cwdFromStore.length > 0) {
    const handleEntry = stores[cwdFromStore];
    if (!handleEntry || typeof handleEntry !== "object") {
      return {
        kind: "error",
        message: `cwd_from_store: no store handle named ${JSON.stringify(cwdFromStore)} in ExecuteRequest.stores`,
      };
    }
    const handle = (handleEntry as { handle?: unknown }).handle;
    if (!handle || typeof handle !== "object") {
      return {
        kind: "error",
        message: `cwd_from_store: store ${JSON.stringify(cwdFromStore)} has no handle struct`,
      };
    }
    const address = (handle as { address?: unknown }).address;
    if (typeof address !== "string" || address.length === 0) {
      return {
        kind: "error",
        message: `cwd_from_store: store ${JSON.stringify(cwdFromStore)} address is not a non-empty string (got ${typeof address})`,
      };
    }
    return validateDirectory(address, `cwd_from_store(${cwdFromStore})`);
  }
  if (cwdOverride && cwdOverride.length > 0) {
    return validateDirectory(cwdOverride, "cwd");
  }
  return { kind: "ok", cwd: undefined };
}

function validateDirectory(path: string, source: string): CwdResolution {
  try {
    const st = statSync(path);
    if (!st.isDirectory()) {
      return {
        kind: "error",
        message: `${source}: path ${JSON.stringify(path)} exists but is not a directory`,
      };
    }
  } catch (e) {
    return {
      kind: "error",
      message: `${source}: stat ${JSON.stringify(path)} failed: ${String(e)}`,
    };
  }
  return { kind: "ok", cwd: path };
}

// renderTemplate retired in the 2026-05-21 userdata collapse.
// Substitution now happens entirely at the rimsky layer
// (`code:graph/attribute/substitution.go::SubstituteValue`); the
// executor consumes resolved prompts verbatim and appends a fixed
// metadata footer for executor-private vars (callback_token,
// resume_payload, resume_reason). See `runAgentReal` above.
