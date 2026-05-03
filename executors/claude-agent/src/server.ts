import { randomUUID } from "node:crypto";
import * as grpc from "@grpc/grpc-js";
import type { Logger } from "pino";
import { loadNodeExecutorProto } from "./proto-loader.js";
import { runAgent, type AgentOutcome } from "./agent-run.js";
import type { CallbackServerHandle } from "./internal-mcp-server.js";
import type { CliRunner } from "./cli-runner.js";
import { createClaudeCliRunner } from "./cli-runner.js";
import type { CliAuthConfig } from "./cli-env.js";
import type { PostAttributesFn } from "./attributes-tools.js";

/**
 * gRPC NodeExecutor implementation. Always responds with the async-handoff
 * pattern: one Heartbeat + AsyncAccepted, close stream, run agent in
 * background, POST final outcome to callback_url.
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
   * Optional override of the HTTP POST function used to deliver the final
   * callback. Tests swap this out to avoid real network calls.
   */
  postCallback?: PostCallbackFn;
  /**
   * Optional override for the writeback POST used by the `attributes_set`
   * MCP tool. Threaded through to `runAgent`.
   */
  postAttributes?: PostAttributesFn;
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
  // Opaque per-node config from the template (spec §5.8). Rimsky never
  // interprets this; the executor reads `model`, `system_prompt`,
  // `user_prompt_template`, `attributes_schema` from here.
  userdata?: unknown;
  // Per-run typed attributes object (spec §5.7). Source-driven fields are
  // pre-populated by rimsky at dispatch.
  attributes?: unknown;
  // Declared JSON Schema for `attributes` (spec §5.7.1).
  attributes_schema?: unknown;
  // Per-store handles keyed by store-config name (spec §12.1). Surfaced to
  // the agent via the userdata bag — no in-process interpretation.
  stores?: Record<string, unknown>;
  callback_url?: string;
  cancel_token?: string;
  // Field number 10 (`resumed`) is reserved on the wire under
  // stores-redesign-v2 (proto reserves both number and name). Resume is
  // universal; the substrate detects resumed-vs-fresh internally.
  run_attempt?: number;
}

type GrpcCall = grpc.ServerWritableStream<ExecuteRequest, unknown>;

/**
 * @agent-contract
 * what: Constructs the gRPC server hosting `NodeExecutor.Execute`, binds to
 *   `host:port`, and wires the async-handoff agent run path.
 * how: `await startGrpcServer(config)`; `shutdown()` stops the server
 *   gracefully.
 * handles: stub-mode short-circuit; JSON-Schema validation of
 *   `attributes_delta` writes; silence / subprocess-exit fault mapping →
 *   Errored outcome; bridging the supervisor's incremental writeback URL
 *   into the internal-MCP `attributes_set` tool.
 * does-not-handle: supervisor-side state transitions, commit, or on_error
 *   routing; those remain in the supervisor process.
 */
export async function startGrpcServer(
  config: GrpcServerConfig,
): Promise<RunningServer> {
  const pkg = loadNodeExecutorProto();
  const server = new grpc.Server();
  const post = config.postCallback ?? defaultPostCallback;
  const cliRunner = config.cliRunner ?? createClaudeCliRunner({
    auth: requireAuth(config.cliAuth),
  });

  server.addService(pkg.rimsky.v1.NodeExecutor.service, {
    Execute: (call: GrpcCall) => handleExecute(call, config, cliRunner, post),
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
  // grpc-js 1.10+: server starts listening automatically after bindAsync; the
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
  const runId = req.node_id ?? randomUUID();
  const logger = config.logger.child({
    run_id: runId,
    node_id: req.node_id,
    node_type: req.node_type,
  });

  // 1) Heartbeat + AsyncAccepted terminal, then close the stream.
  call.write({
    heartbeat: {
      timestamp_ms: Date.now(),
      note: "accepted",
    },
  });
  call.write({
    async_accepted: {
      async_ack_id: ackId,
      expected_completion_ms: 0,
    },
  });
  call.end();

  // 2) Run the agent in the background; deliver final outcome via HTTP POST.
  void runAndCallback(req, ackId, runId, config, cliRunner, post, logger);
}

async function runAndCallback(
  req: ExecuteRequest,
  ackId: string,
  runId: string,
  config: GrpcServerConfig,
  cliRunner: CliRunner,
  post: PostCallbackFn,
  logger: Logger,
): Promise<void> {
  try {
    const userdata = toRecord(req.userdata);
    const attributes = toRecord(req.attributes);
    const outcome = await runAgent({
      runId,
      nodeId: req.node_id ?? runId,
      nodeType: req.node_type ?? "unknown",
      model: stringOr(userdata.model, "claude-sonnet-4-5"),
      systemPrompt: stringOr(userdata.system_prompt, ""),
      userPromptTemplate: stringOr(userdata.user_prompt_template, ""),
      attributesSchema: req.attributes_schema ?? {},
      attributes,
      templateVars: {
        userdata,
        attributes,
      },
      stores: req.stores ?? {},
      cwdFromStore: stringOrUndefined(userdata.cwd_from_store),
      cwdOverride: stringOrUndefined(userdata.cwd),
      callbackUrl: req.callback_url ?? "",
      cancelToken: req.cancel_token ?? "",
      cliRunner,
      callback: config.callback,
      silenceTimeoutMs: config.silenceTimeoutMs,
      logger,
      postAttributes: config.postAttributes,
    });
    const body = outcomeToCallbackBody(outcome);
    if (req.callback_url) {
      await post(buildCallbackUrl(req.callback_url, ackId), body, logger);
    } else {
      logger.warn({ outcome: outcome.kind }, "no callback_url; outcome dropped");
    }
  } catch (e) {
    logger.error({ error: String(e) }, "agent run failed unexpectedly");
    if (req.callback_url) {
      await post(
        buildCallbackUrl(req.callback_url, ackId),
        {
          type: "errored",
          error_class: "executor_internal_error",
          payload: { error: String(e) },
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

function outcomeToCallbackBody(
  outcome: AgentOutcome,
): Record<string, unknown> {
  if (outcome.kind === "complete") {
    // Spec §12.2/§12.3: the Complete callback carries `attributes_delta`
    // (a map merged into rimsky_node_attributes.data on commit). The
    // legacy `result` field is retired.
    return {
      type: "complete",
      attributes_delta: outcome.attributesDelta,
      changed: outcome.changed,
      change_summary: outcome.changeSummary,
    };
  }
  if (outcome.kind === "blocked") {
    return {
      type: "blocked",
      reason: outcome.reason,
      context: outcome.context,
    };
  }
  return {
    type: "errored",
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

function stringOrUndefined(v: unknown): string | undefined {
  return typeof v === "string" && v.length > 0 ? v : undefined;
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
