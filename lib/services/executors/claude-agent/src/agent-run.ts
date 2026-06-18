// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { randomUUID } from "node:crypto";
import { statSync } from "node:fs";
import type { Logger } from "pino";
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
import { verifyRequiredSignoffs } from "./signoff.js";

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
      kind: "park_requested";
      reason: string;
      reasonNote: string;
      attributesDelta: Record<string, unknown> | null;
      resumeAt: Date | null;
      sessionToken: string;
    };

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
  nodeId: string;
  nodeType: string;
  model: string;
  systemPrompt: string;
  userPrompt: string;
  attributesSchema: unknown;
  attributes: Record<string, unknown>;
  stores?: Record<string, unknown>;
  cwdFromStore?: string;
  cwdOverride?: string;
  cliConfig?: {
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
  };
  mcpCatalog?: McpCatalog;
  mcpAllowInline?: boolean;
  dispatchId?: string;
  callbackUrl: string;
  cancelToken: string;
  cliRunner: CliRunner;
  callback: CallbackServerHandle;
  silenceTimeoutMs: number;
  logger: Logger;
  sessionToken: string;
  postAttributes?: PostAttributesFn;
}

export async function runAgent(opts: AgentRunOptions): Promise<AgentOutcome> {
  const attrs = opts.attributes ?? {};
  const isProbe =
    (attrs.stub_probe === true || attrs.probe_park === true) && stubModeEnabled();
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

  if (attrs.probe_park === true) {
    const rawReason = attrs.park_reason;
    let parkReason: string;
    if (rawReason === undefined) {
      parkReason = "await_callback";
    } else if (rawReason === "await_callback" || rawReason === "snooze") {
      parkReason = rawReason;
    } else {
      return {
        kind: "errored",
        errorClass: "agent/attribute_invalid",
        payload: {
          reason: `park_reason must be one of "await_callback" | "snooze", got ${JSON.stringify(rawReason)}`,
        },
      };
    }
    const reasonNote =
      typeof attrs.park_reason_note === "string" ? attrs.park_reason_note : "";
    const resumeAt =
      parkReason === "snooze" ? new Date(Date.now() + 30_000) : null;
    return {
      kind: "park_requested",
      reason: parkReason,
      reasonNote,
      attributesDelta: null,
      resumeAt,
      sessionToken: "",
    };
  }

  const isProbe = attrs.stub_probe === true;
  if (!isProbe) {
    return {
      kind: "complete",
      attributesDelta: { stub: true, session_token: opts.runId },
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
      attributesDelta: {
        ...(stubResponse as Record<string, unknown>),
        session_token: opts.runId,
      },
      changed: true,
      changeSummary: "stub",
    };
  }
  return {
    kind: "complete",
    attributesDelta: { stub: true, session_token: opts.runId },
    changed: true,
    changeSummary: "stub",
  };
}

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
    sessionToken,
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

  const callbackToken = randomUUID();

  const renderedSystem = systemPrompt;
  const renderedUser =
    userPrompt +
    "\n\n---\n" +
    `callback_token: ${callbackToken}\n` +
    `binding_id: ${dispatchId ?? ""}\n` +
    "---\n";

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
  const effectiveCallback = dispatchMcp;

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

  const post = postAttributes ?? defaultPostAttributes;
  const writebackUrl = callbackUrl
    ? buildAttributesWritebackUrl(callbackUrl, runId)
    : "";
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

  const maxSchemaCorrections =
    typeof cliConfig?.maxSchemaCorrections === "number" && cliConfig.maxSchemaCorrections >= 0
      ? cliConfig.maxSchemaCorrections
      : 3;
  let schemaCorrectionFailures = 0;

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
      signoffs,
      scheduleTeardown,
    ) => {
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
      schemaCorrectionFailures = 0;

      const effectiveBag: Record<string, unknown> = {
        ...accumulatedWriteback,
        ...(attributesDelta ?? {}),
        session_token: runId,
      };

      const required = cliConfig?.requiredSignoffs ?? [];
      if (required.length > 0) {
        if (!dispatchId || dispatchId.length === 0) {
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
        signoffFailures = 0;
      }

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
          attributesDelta: null,
          resumeAt: parsedResumeAt,
          sessionToken: runId,
        });
      });
    },
    onAttributesSet,
  });

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
  const templateAllowed = cliConfig?.allowedTools ?? [];
  const allowedToolsUnion =
    templateAllowed.length > 0 || hostAllowed.length > 0
      ? [...templateAllowed, ...hostAllowed]
      : undefined;

  let handle: CliHandle;
  try {
    if (
      sessionToken &&
      sessionToken.length > 0 &&
      cliRunner.resume !== undefined
    ) {
      logger.info(
        { runId, session_token: sessionToken },
        "cli.resume_with_session_token",
      );
      handle = await cliRunner.resume({
        sessionId: sessionToken,
        prompt: renderedUser,
        tools: [
          { kind: "mcp-http", name: "rimsky-callback", url: effectiveCallback.url },
          ...hostServers,
        ],
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
        sessionId: runId,
        bare: cliConfig?.bare,
        permissionMode: cliConfig?.permissionMode,
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
    await tearDownCatalogServers();
    return {
      kind: "errored",
      errorClass: "agent/cli_spawn_failed",
      payload: { error: String(e) },
    };
  }
  handleRef = handle;

  let lastStdoutAt = Date.now();
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
    if (resolved) return;

    const handleRateLimits = cliConfig?.handleRateLimits !== false;
    if (
      exitCode !== 0 &&
      exitCode !== null &&
      stderrBuf.length > 0
    ) {
      const signalRL = detectRateLimit(stderrBuf, new Date());
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
          reason: "snooze",
          reasonNote:
            signalRL.reason !== ""
              ? signalRL.reason
              : "claude cli rate-limit detected; resume_at=" +
                (signalRL.resumeAt?.toISOString() ?? "indefinite"),
          attributesDelta: null,
          resumeAt: signalRL.resumeAt,
          sessionToken: runId,
        });
        return;
      }
    }

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
        if (!resolved) {
          let graceTimer: NodeJS.Timeout | null = null;
          await Promise.race([
            teardownDone,
            new Promise<void>((r) => {
              graceTimer = setTimeout(r, 2000);
            }),
          ]);
          if (graceTimer) clearTimeout(graceTimer);
          await new Promise<void>((r) => setImmediate(r));
        }
        if (resolved) return;
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

    const classified = classifyAgentError(stderrBuf, exitCode);
    safeResolve({
      kind: "errored",
      errorClass: classified?.errorClass ?? "agent/subprocess_exit/before_complete",
      payload: { exitCode, signal },
    });
  })();

  try {
    const outcome = await outcomePromise;
    return outcome;
  } finally {
    silenceStopped = true;
    teardownResolve();
    await silenceLoop.catch(() => {});
    effectiveCallback.registry.release(callbackToken);
    void closeDispatchMcp();
    await tearDownCatalogServers();
  }
}

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

function autoAllow(name: string, allowedTools?: string[]): string[] {
  return allowedTools && allowedTools.length > 0
    ? allowedTools.map((t) => `mcp__${name}__${t}`)
    : [`mcp__${name}`];
}

type CwdResolution =
  | { kind: "ok"; cwd: string | undefined }
  | { kind: "error"; message: string };

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

