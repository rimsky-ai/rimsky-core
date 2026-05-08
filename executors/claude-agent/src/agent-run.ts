// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { randomUUID } from "node:crypto";
import { statSync } from "node:fs";
import type { Logger } from "pino";
// ajv ships CJS — under ESM+NodeNext we reach the constructor through the
// interop namespace; the `.default` arm handles the nested form.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
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

/**
 * Outcome the executor relays back to the rimsky supervisor via the async
 * callback URL. Per spec §12.2 the legacy `result` field has been retired in
 * favour of `attributes_delta`.
 *
 * - `complete`: terminal success. `attributesDelta` is the terminal-final
 *   writeback (may be `null` when the executor used the incremental
 *   `attributes_set` callback path; the supervisor already has that data).
 * - `blocked`: terminal `Blocked`.
 * - `errored`: terminal `Errored`.
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
  | { kind: "errored"; errorClass: string; payload: unknown };

export interface AgentRunOptions {
  runId: string;
  /**
   * Supervisor-side `node_id` — used as the path segment on the incremental
   * writeback URL (`{callback_url}/v1/attributes/{node_id}`).
   */
  nodeId: string;
  nodeType: string;
  model: string;
  systemPrompt: string;
  userPromptTemplate: string;
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
   * Userdata bag from the template (per spec §5.8). Rimsky never parses
   * this; the executor reads `model`, `system_prompt`, etc. from here. The
   * `templateVars.userdata` namespace below is what `{{userdata.x}}`
   * resolves against in renderTemplate.
   */
  templateVars: {
    userdata: Record<string, unknown>;
    attributes: Record<string, unknown>;
  };
  /**
   * Per-store handles delivered in `ExecuteRequest.stores` (spec §19.1).
   * Keyed by store-config name; each entry is the unwrapped
   * `{kind, handle: {address, payload, alias, intent}}` shape. Opaque
   * to rimsky; the executor unwraps per its store-specific knowledge.
   */
  stores?: Record<string, unknown>;
  /**
   * Optional store-config name from `userdata.cwd_from_store`. When set,
   * the executor reads `stores[<name>].handle.address` (which the
   * filesystem store fills with an absolute path) and uses it as the
   * spawned CLI's cwd. Validated as an existing directory before spawn;
   * any mismatch errors as `invalid_cwd_from_store`.
   */
  cwdFromStore?: string;
  /**
   * Optional raw cwd from `userdata.cwd`. Override-of-last-resort for
   * deployments that pin a static workdir without going through a store.
   * Lower priority than `cwdFromStore`.
   */
  cwdOverride?: string;
  /**
   * Per-template CLI tuning sourced from `userdata.cli.*`. Forwarded
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
   * Optional override for the writeback POST function used by the
   * `attributes_set` MCP tool. Tests swap this out to avoid real network
   * calls.
   */
  postAttributes?: PostAttributesFn;
}

export async function runAgent(opts: AgentRunOptions): Promise<AgentOutcome> {
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
  return {
    kind: "complete",
    attributesDelta: { stub: true },
    changed: true,
    changeSummary: "stub",
  };
}

async function runAgentReal(opts: AgentRunOptions): Promise<AgentOutcome> {
  const {
    runId,
    nodeId,
    model,
    systemPrompt,
    userPromptTemplate,
    attributesSchema,
    attributes,
    templateVars,
    stores,
    cwdFromStore,
    cwdOverride,
    cliConfig,
    callbackUrl,
    cancelToken,
    cliRunner,
    callback,
    silenceTimeoutMs,
    logger,
    postAttributes,
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

  // Generate the per-run callback token before rendering prompts so it can
  // be substituted into the system / user prompt via `{{callback_token}}`.
  // The agent (Claude Code CLI subprocess) needs this token to call any
  // rimsky-callback MCP tool. Injecting it via the prompt avoids requiring
  // the agent to have shell access to read `RIMSKY_CALLBACK_TOKEN` from env
  // (the env var is still set on the child for tools that DO have shell).
  const callbackToken = randomUUID();
  const promptVars = {
    ...templateVars,
    callback_token: callbackToken,
  };

  const renderedSystem = renderTemplate(systemPrompt, promptVars);
  const renderedUser = renderTemplate(userPromptTemplate, promptVars);

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
  const post = postAttributes ?? defaultPostAttributes;
  const writebackUrl = callbackUrl
    ? buildAttributesWritebackUrl(callbackUrl, nodeId)
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
          return {
            status: "rejected",
            errors: { attributes_delta: ["must be an object"] },
          };
        }
        try {
          JSON.stringify(attributesDelta);
        } catch (e) {
          return {
            status: "rejected",
            errors: {
              attributes_delta: [`unserializable_attributes_delta: ${String(e)}`],
            },
          };
        }
        if (validateAttributes) {
          // The delta merged on top of the dispatch-time attributes is
          // what the supervisor will validate authoritatively; we do a
          // best-effort local check on the same merged shape.
          const merged = { ...attributes, ...attributesDelta };
          if (!validateAttributes(merged)) {
            const errs =
              (validateAttributes as unknown as { errors?: unknown[] }).errors ?? [];
            return {
              status: "rejected",
              errors: {
                attributes_delta: errs.map((e) => JSON.stringify(e)),
              },
            };
          }
        }
      }
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
    onAttributesSet,
  });

  let handle: CliHandle;
  try {
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
  handle.onStdout((chunk) => {
    lastStdoutAt = Date.now();
    logger.info({ runId, chunk: chunk.slice(0, 2000) }, "cli.stdout");
  });
  handle.onStderr((chunk) => {
    logger.warn({ runId, chunk: chunk.slice(0, 2000) }, "cli.stderr");
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
 * Resolve the spawn cwd from store handles + userdata hints.
 *
 * Precedence:
 *   1. `cwdFromStore` — look up `stores[<name>].handle.address`. The
 *      filesystem store sets this to an absolute path. Must be a string,
 *      and must point to an existing directory at spawn time.
 *   2. `cwdOverride` — raw path from `userdata.cwd`. Validated the same
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

/**
 * Minimal `{{ns.key}}` substitution for system / user prompts. After the
 * stores-redesign (spec §5.7) the only supported namespaces are
 * `userdata` and `attributes`. Substitution against unknown namespaces is
 * preserved verbatim.
 *
 * @source rimsky/src/supervisor/agentic-runner.ts:renderTemplate
 */
export function renderTemplate(
  tpl: string,
  vars: {
    userdata: Record<string, unknown>;
    attributes: Record<string, unknown>;
    /**
     * The per-run rimsky-callback token. Exposed as a bare `{{callback_token}}`
     * placeholder so templates can inject it into the system / user prompt
     * without the agent needing shell access to read `RIMSKY_CALLBACK_TOKEN`
     * from env. Optional — older callers don't pass it; the placeholder is
     * preserved verbatim in that case.
     */
    callback_token?: string;
  },
): string {
  let out = tpl.replace(
    /\{\{(userdata|attributes)\.([^}]+)\}\}/g,
    (_, ns: "userdata" | "attributes", key: string) => {
      const bag = vars[ns];
      const v = bag[key];
      if (v === undefined) return `{{${ns}.${key}}}`;
      return typeof v === "string" ? v : JSON.stringify(v);
    },
  );
  if (vars.callback_token !== undefined) {
    out = out.replace(/\{\{callback_token\}\}/g, vars.callback_token);
  }
  return out;
}
