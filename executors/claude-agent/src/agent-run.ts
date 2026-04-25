import { randomUUID } from "node:crypto";
import type { Logger } from "pino";
// ajv ships CJS — under ESM+NodeNext we reach the constructor through the
// interop namespace; the `.default` arm handles the nested form.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
import * as AjvNs from "ajv";
type AjvCtor = new (opts?: object) => { compile: (schema: object) => (v: unknown) => boolean };
const Ajv: AjvCtor = (((AjvNs as unknown) as { default?: AjvCtor }).default ??
  ((AjvNs as unknown) as AjvCtor));
import type { CliRunner, CliHandle } from "./cli-runner.js";
import type { CallbackServerHandle } from "./internal-mcp-server.js";

/**
 * Outcome the executor relays back to the rimsky supervisor via the async
 * callback URL. Derived from the semantics of
 * {@link https://github.com/fallguy/rimsky "rimsky agentic-runner"} but stripped
 * of storage / queue / state-machine side-effects (those are the supervisor's
 * concern, not the executor's).
 *
 * @source rimsky/src/supervisor/agentic-runner.ts (semantic port)
 */
export type AgentOutcome =
  | {
      kind: "complete";
      result: unknown;
      changed: boolean;
      changeSummary: string | null;
    }
  | { kind: "blocked"; reason: string; context: unknown }
  | { kind: "errored"; errorClass: string; payload: unknown };

export interface AgentRunOptions {
  runId: string;
  nodeType: string;
  model: string;
  systemPrompt: string;
  userPromptTemplate: string;
  resultSchema: unknown;
  templateVars: {
    userdata: Record<string, unknown>;
    params: Record<string, unknown>;
    deps: Record<string, unknown>;
    reads: Record<string, unknown>;
  };
  cliRunner: CliRunner;
  callback: CallbackServerHandle;
  silenceTimeoutMs: number;
  logger: Logger;
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
    result: { stub: true },
    changed: true,
    changeSummary: "stub",
  };
}

async function runAgentReal(opts: AgentRunOptions): Promise<AgentOutcome> {
  const {
    runId,
    model,
    systemPrompt,
    userPromptTemplate,
    resultSchema,
    templateVars,
    cliRunner,
    callback,
    silenceTimeoutMs,
    logger,
  } = opts;

  const renderedSystem = renderTemplate(systemPrompt, templateVars);
  const renderedUser = renderTemplate(userPromptTemplate, templateVars);

  const callbackToken = randomUUID();

  // Lazily compile the result schema if one is provided; ajv throws on invalid
  // schema shape which we surface as an errored outcome before we spawn.
  const ajv = new Ajv({ allErrors: true, strict: false });
  let validateResult: ((v: unknown) => boolean) | null = null;
  let schemaErrors: string[] = [];
  if (
    resultSchema &&
    typeof resultSchema === "object" &&
    Object.keys(resultSchema as object).length > 0
  ) {
    try {
      validateResult = ajv.compile(resultSchema as object);
    } catch (e) {
      schemaErrors.push(`invalid result_schema: ${String(e)}`);
    }
  }

  if (schemaErrors.length > 0) {
    return {
      kind: "errored",
      errorClass: "invalid_result_schema",
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

  callback.registry.register(callbackToken, {
    runId,
    resultSchema: resultSchema ?? {},
    onComplete: async (result, changed, changeSummary, scheduleTeardown) => {
      // Basic validation: must be an object.
      if (result === undefined || result === null || typeof result !== "object") {
        return {
          status: "rejected",
          errors: { result: ["must be an object"] },
        };
      }
      // Serializability gate.
      try {
        JSON.stringify(result);
      } catch (e) {
        return {
          status: "rejected",
          errors: { result: [`unserializable_result: ${String(e)}`] },
        };
      }
      // JSON-schema validation when a schema was supplied.
      if (validateResult && !validateResult(result)) {
        const errs = (validateResult as unknown as { errors?: unknown[] }).errors ?? [];
        return {
          status: "rejected",
          errors: { result: errs.map((e) => JSON.stringify(e)) },
        };
      }
      scheduleTeardown(async () => {
        await teardownCli();
        safeResolve({
          kind: "complete",
          result,
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
  });

  let handle: CliHandle;
  try {
    handle = await cliRunner.spawn({
      model,
      systemPrompt: renderedSystem,
      userPrompt: renderedUser,
      tools: [
        { kind: "mcp-http", name: "rimsky-callback", url: callback.url },
      ],
      env: {
        RIMSKY_CALLBACK_URL: callback.url,
        RIMSKY_CALLBACK_TOKEN: callbackToken,
      },
    });
  } catch (e) {
    callback.registry.release(callbackToken);
    return {
      kind: "errored",
      errorClass: "cli_spawn_failed",
      payload: { error: String(e) },
    };
  }
  handleRef = handle;

  let lastStdoutAt = Date.now();
  handle.onStdout(() => {
    lastStdoutAt = Date.now();
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

  void (async () => {
    const { exitCode, signal } = await handle.waitExit();
    let raceTimer: NodeJS.Timeout | null = null;
    await Promise.race([
      teardownDone,
      new Promise<void>((r) => {
        raceTimer = setTimeout(r, 2000);
      }),
    ]);
    if (raceTimer) clearTimeout(raceTimer);
    if (teardownInProgress) return;
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
    callback.registry.release(callbackToken);
  }
}

/**
 * Minimal `{{ns.key}}` substitution for system / user prompts. If `ns.key` is
 * not present in `vars`, the literal `{{...}}` is preserved.
 *
 * @source rimsky/src/supervisor/agentic-runner.ts:renderTemplate
 */
export function renderTemplate(
  tpl: string,
  vars: {
    userdata: Record<string, unknown>;
    params: Record<string, unknown>;
    deps: Record<string, unknown>;
    reads: Record<string, unknown>;
  },
): string {
  return tpl.replace(
    /\{\{(userdata|params|deps|reads)\.([^}]+)\}\}/g,
    (_, ns: "userdata" | "params" | "deps" | "reads", key: string) => {
      const bag = vars[ns];
      const v = bag[key];
      if (v === undefined) return `{{${ns}.${key}}}`;
      return typeof v === "string" ? v : JSON.stringify(v);
    },
  );
}
