import { randomUUID } from "node:crypto";
import Fastify from "fastify";
import type { FastifyInstance } from "fastify";
import type { Logger } from "pino";
import type { CliRunner } from "./cli-runner.js";
import type { CallbackServerHandle } from "./internal-mcp-server.js";
import { createClaudeCliRunner } from "./cli-runner.js";
import { runAgent, type AgentOutcome } from "./agent-run.js";
import type { PostCallbackFn } from "./server.js";
import { defaultPostCallback } from "./server.js";

/**
 * HTTP+JSON bridge. Callers that can't speak gRPC POST to `/execute` with an
 * ExecuteRequest-shaped body; the bridge returns `{ async_ack_id }`
 * immediately and posts the outcome to `callback_url` when the agent finishes.
 *
 * This is primarily a debug / integration surface — rimsky supervisors
 * normally use the gRPC transport.
 */
export interface HttpBridgeConfig {
  host: string;
  port: number;
  callback: CallbackServerHandle;
  cliRunner?: CliRunner;
  silenceTimeoutMs: number;
  logger: Logger;
  postCallback?: PostCallbackFn;
}

export interface RunningHttpBridge {
  address: string;
  shutdown(): Promise<void>;
}

interface ExecuteBody {
  node_id?: string;
  instance_id?: string;
  node_type?: string;
  userdata?: unknown;
  instance_params?: unknown;
  deps_data?: Record<string, unknown>;
  reads_data?: Record<string, unknown>;
  callback_url?: string;
}

export async function startHttpBridge(
  config: HttpBridgeConfig,
): Promise<RunningHttpBridge> {
  const app: FastifyInstance = Fastify({
    logger: false,
  });
  const post = config.postCallback ?? defaultPostCallback;
  const cliRunner = config.cliRunner ?? createClaudeCliRunner();

  app.get("/healthz", async () => ({ ok: true }));

  app.post("/execute", async (req, reply) => {
    const body = (req.body ?? {}) as ExecuteBody;
    const ackId = randomUUID();
    const runId = body.node_id ?? randomUUID();
    const log = config.logger.child({ run_id: runId, node_id: body.node_id });

    void runAndCallback(body, ackId, runId, config, cliRunner, post, log);

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
  runId: string,
  config: HttpBridgeConfig,
  cliRunner: CliRunner,
  post: PostCallbackFn,
  logger: Logger,
): Promise<void> {
  try {
    const userdata = toRecord(body.userdata);
    const params = toRecord(body.instance_params);
    const outcome = await runAgent({
      runId,
      nodeType: body.node_type ?? "unknown",
      model: stringOr(userdata.model, "claude-sonnet-4-5"),
      systemPrompt: stringOr(userdata.system_prompt, ""),
      userPromptTemplate: stringOr(userdata.user_prompt_template, ""),
      resultSchema: userdata.result_schema ?? {},
      templateVars: {
        userdata,
        params,
        deps: body.deps_data ?? {},
        reads: body.reads_data ?? {},
      },
      cliRunner,
      callback: config.callback,
      silenceTimeoutMs: config.silenceTimeoutMs,
      logger,
    });
    const cb = outcomeToCallbackBody(outcome, ackId);
    if (body.callback_url) {
      await post(body.callback_url, cb, logger);
    } else {
      logger.warn({ outcome: outcome.kind }, "no callback_url; outcome dropped");
    }
  } catch (e) {
    logger.error({ error: String(e) }, "agent run failed unexpectedly");
    if (body.callback_url) {
      await post(
        body.callback_url,
        {
          async_ack_id: ackId,
          kind: "errored",
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
    return {
      async_ack_id: ackId,
      kind: "complete",
      result: outcome.result,
      changed: outcome.changed,
      change_summary: outcome.changeSummary,
    };
  }
  if (outcome.kind === "blocked") {
    return {
      async_ack_id: ackId,
      kind: "blocked",
      reason: outcome.reason,
      context: outcome.context,
    };
  }
  return {
    async_ack_id: ackId,
    kind: "errored",
    error_class: outcome.errorClass,
    payload: outcome.payload,
  };
}

function toRecord(v: unknown): Record<string, unknown> {
  if (v && typeof v === "object" && !Array.isArray(v)) {
    return v as Record<string, unknown>;
  }
  return {};
}

function stringOr(v: unknown, fallback: string): string {
  return typeof v === "string" ? v : fallback;
}
