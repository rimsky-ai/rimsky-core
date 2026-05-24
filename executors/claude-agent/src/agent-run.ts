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
import type { CliRunner, CliHandle } from "./cli-runner.js";
import { startInternalMcpServer, type CallbackServerHandle } from "./internal-mcp-server.js";
import {
  buildAttributesWritebackUrl,
  defaultPostAttributes,
  type PostAttributesFn,
} from "./attributes-tools.js";
import { detectRateLimit } from "./rate-limit.js";

/**
 * Outcome the executor relays back to the rimsky supervisor via the async
 * callback URL. Per spec §12.2 the legacy `result` field has been retired in
 * favour of `attributes_delta`.
 *
 * - `complete`: terminal success — maps to a StreamClose `Success` outcome on
 *   the wire. `attributesDelta` is the terminal-final writeback (may be `null`
 *   when the executor used the incremental `attributes_set` callback path; the
 *   supervisor already has that data).
 * - `blocked`: maps to a StreamClose `Error{error_class:"executor_blocked"}`
 *   outcome on the wire (post-E.2 the pre-rename Blocked variant collapsed
 *   into Error with the reserved `executor_blocked` class).
 * - `errored`: maps to a StreamClose `Error{error_class}` outcome on the wire.
 *
 * @source rimsky/src/supervisor/agentic-runner.ts (semantic port)
 */
export type AgentOutcome =
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
    };

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
   * any mismatch errors as `invalid_cwd_from_store`.
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
     * with a StreamClose `Error{error_class: "schema_validation_failed"}`
     * outcome on the wire. Default 3.
     */
    maxSchemaCorrections?: number;
  };
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
        errorClass: "invalid_attribute",
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
        errorClass: "invalid_attribute",
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
      errorClass: "invalid_cwd_from_store",
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
  const renderedUser =
    userPrompt +
    "\n\n---\n" +
    `callback_token: ${callbackToken}\n` +
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
      errorClass: "invalid_attributes_schema",
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
  const onAttributesSet = async (
    delta: Record<string, unknown>,
  ): Promise<{ status: number }> => {
    if (!writebackUrl) {
      logger.warn({ runId }, "attributes_set called but no callback_url; dropping");
      return { status: 503 };
    }
    try {
      return await post(writebackUrl, { delta }, cancelToken);
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
  // "schema_validation_failed"}` outcome on the wire.
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
          errorClass: "schema_validation_failed",
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

  effectiveCallback.registry.register(callbackToken, {
    runId,
    attributesAtSpawn: attributes,
    cancelToken,
    nodeId,
    callbackUrl,
    onComplete: async (
      attributesDelta,
      changed,
      changeSummary,
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
      scheduleTeardown(async () => {
        await teardownCli();
        safeResolve({
          kind: "complete",
          attributesDelta,
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
        ],
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
        allowedTools: cliConfig?.allowedTools,
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
    return {
      kind: "errored",
      errorClass: "cli_spawn_failed",
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
          errorClass: "silence_timeout",
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
      handleRateLimits &&
      exitCode !== 0 &&
      exitCode !== null &&
      stderrBuf.length > 0
    ) {
      const signalRL = detectRateLimit(stderrBuf, new Date());
      if (signalRL.detected) {
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
          ],
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
        if (resolved) return; // retry's MCP callback fired — outcome already set
        safeResolve({
          kind: "errored",
          errorClass: "subprocess_exit_before_complete",
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
          errorClass: "subprocess_exit_before_complete",
          payload: { exitCode, signal, retry_failed: String(err) },
        });
        return;
      }
    }

    safeResolve({
      kind: "errored",
      errorClass: "subprocess_exit_before_complete",
      payload: { exitCode, signal },
    });
  })();

  try {
    return await outcomePromise;
  } finally {
    silenceStopped = true;
    teardownResolve();
    await silenceLoop.catch(() => {});
    effectiveCallback.registry.release(callbackToken);
    void closeDispatchMcp();
  }
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
