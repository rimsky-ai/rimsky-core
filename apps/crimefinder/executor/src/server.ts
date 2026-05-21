import { randomUUID } from "node:crypto";
import * as grpc from "@grpc/grpc-js";
import type { Logger } from "pino";
import type { AgentOutcome } from "./agent-types.js";
import { runAgent, DispatchedStore } from "./agent-run.js";
import type { CliAuthConfig } from "./cli-env.js";
import { loadExecutorProtos } from "./proto-loader.js";
import { buildCapabilitiesResponse } from "./capabilities.js";

export type PostCallbackFn = (
  url: string,
  body: Record<string, unknown>,
  logger: Logger,
) => Promise<void>;

export interface ExecutorServerConfig {
  host: string;
  port: number;
  silenceTimeoutMs: number;
  stubMode: boolean;
  cliAuth?: CliAuthConfig;
  cliBinPath?: string;
  post?: PostCallbackFn;
  logger: Logger;
}

export interface RunningExecutorServer {
  address: string;
  shutdown(): Promise<void>;
}

export function buildCallbackUrl(base: string, ackId: string): string {
  return `${base.replace(/\/+$/, "")}/v1/callback/${encodeURIComponent(ackId)}`;
}

function encodeBase64(bytes: Uint8Array): string {
  return Buffer.from(bytes).toString("base64");
}

export function outcomeToCallbackBody(outcome: AgentOutcome): Record<string, unknown> {
  const events = outcome.events.map((e) => ({
    name: e.event,
    payload: encodeBase64(new TextEncoder().encode(JSON.stringify(e))),
  }));
  if (outcome.variant === "success") {
    return {
      events,
      success: {
        attributes_delta: outcome.attributesDelta ?? {},
        changed: outcome.changed ?? false,
        change_summary: outcome.changeSummary ?? "",
      },
    };
  }
  if (outcome.variant === "error") {
    return {
      events,
      error: {
        error_class: outcome.errorClass,
        payload: outcome.payload ? encodeBase64(outcome.payload) : null,
      },
    };
  }
  return {
    events,
    park: {
      reason: outcome.reason,
      reason_note: outcome.reasonNote ?? "",
      resume_at: outcome.resumeAt ?? "",
    },
  };
}

async function defaultPost(url: string, body: Record<string, unknown>, logger: Logger): Promise<void> {
  const res = await fetch(url, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    logger.warn({ url, status: res.status }, "callback_post_non_ok");
  }
}

function unwrapStores(raw: Record<string, { address?: Uint8Array | Buffer }>): DispatchedStore[] {
  const out: DispatchedStore[] = [];
  for (const [alias, val] of Object.entries(raw ?? {})) {
    const addr = val?.address;
    if (!addr) continue;
    out.push({
      alias,
      address: addr instanceof Uint8Array ? addr : new Uint8Array(addr),
    });
  }
  return out;
}

function unwrapUserdata(raw: unknown): Record<string, unknown> {
  if (!raw || typeof raw !== "object") return {};
  // proto-loader keepCase decodes google.protobuf.Struct as { fields: { k: Value } }.
  // Otherwise it's a plain record we can pass through.
  const obj = raw as Record<string, unknown> & { fields?: Record<string, unknown> };
  if (obj.fields && typeof obj.fields === "object") {
    return unwrapStruct(obj);
  }
  return obj;
}

function unwrapStruct(s: { fields?: Record<string, unknown> }): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, raw] of Object.entries(s.fields ?? {})) out[k] = unwrapValue(raw);
  return out;
}

function unwrapValue(v: unknown): unknown {
  if (v === null || v === undefined) return null;
  if (typeof v !== "object") return v;
  const o = v as Record<string, unknown>;
  if ("kind" in o) {
    const kind = o.kind as string;
    if (kind === "string_value" || kind === "stringValue") return o.string_value ?? o.stringValue;
    if (kind === "number_value" || kind === "numberValue") return o.number_value ?? o.numberValue;
    if (kind === "bool_value" || kind === "boolValue") return o.bool_value ?? o.boolValue;
    if (kind === "null_value" || kind === "nullValue") return null;
    if (kind === "list_value" || kind === "listValue") {
      const lv = (o.list_value ?? o.listValue) as { values?: unknown[] } | undefined;
      return (lv?.values ?? []).map((x) => unwrapValue(x));
    }
    if (kind === "struct_value" || kind === "structValue") {
      return unwrapStruct((o.struct_value ?? o.structValue) as { fields?: Record<string, unknown> });
    }
  }
  // fallback: present as-is
  return o;
}

export async function startExecutorGrpcServer(
  cfg: ExecutorServerConfig,
): Promise<RunningExecutorServer> {
  const pkg = loadExecutorProtos();
  const server = new grpc.Server();
  const post = cfg.post ?? defaultPost;

  type ServerCall = grpc.ServerWritableStream<unknown, unknown>;

  server.addService(pkg.rimsky.v1.Executor.service, {
    Execute: ((call: ServerCall) => {
      const req = call.request as {
        node_id?: string;
        node_type?: string;
        dispatch_id?: string;
        instance_id?: string;
        stores?: Record<string, { address?: Uint8Array | Buffer }>;
        userdata?: unknown;
        callback_url?: string;
      };
      const ackId = randomUUID();
      const runId = req.node_id ?? randomUUID();
      const logger = cfg.logger.child({
        run_id: runId,
        dispatch_id: req.dispatch_id,
        node_type: req.node_type,
      });

      call.write({
        heartbeat: { timestamp_ms: Date.now(), note: "accepted" },
      });
      call.write({
        stream_close: {
          await_async: { async_ack_id: ackId, expected_completion_ms: 0 },
        },
      });
      call.end();

      void (async () => {
        try {
          const stores = unwrapStores(req.stores ?? {});
          const userdata = unwrapUserdata(req.userdata);
          const outcome = await runAgent({
            dispatchId: req.dispatch_id ?? runId,
            runId,
            userdata,
            stores,
            callbackUrl: req.callback_url ?? "",
            silenceTimeoutMs: cfg.silenceTimeoutMs,
            stubMode: cfg.stubMode,
            cliAuth: cfg.cliAuth,
            cliBinPath: cfg.cliBinPath,
            logger,
          });
          const body = outcomeToCallbackBody(outcome);
          if (req.callback_url) {
            await post(buildCallbackUrl(req.callback_url, ackId), body, logger);
          } else {
            logger.warn({ variant: outcome.variant }, "no_callback_url_outcome_dropped");
          }
        } catch (e) {
          logger.error({ err: String(e) }, "execute_fatal");
          if (req.callback_url) {
            await post(
              buildCallbackUrl(req.callback_url, ackId),
              {
                events: [],
                error: { error_class: "tool_error", payload: null },
              },
              logger,
            ).catch(() => undefined);
          }
        }
      })();
    }) as unknown as grpc.handleServerStreamingCall<unknown, unknown>,
  });

  // Minimal ExecutorObservability so the supervisor's startup handshake
  // works. The userdata_schema + declared_events come from a single
  // module (capabilities.ts / userdata-schema.ts) so changes to either
  // stay in lockstep.
  const caps = buildCapabilitiesResponse();
  server.addService(pkg.rimsky.v1.ExecutorObservability.service, {
    Capabilities: ((_call: grpc.ServerUnaryCall<unknown, unknown>, cb: grpc.sendUnaryData<unknown>) => {
      cb(null, {
        supports_trace_get: false,
        supports_trace_stream: false,
        retention_after_terminal_seconds: 3600,
        http_bridge_url: caps.http_bridge_url,
        userdata_schema: Buffer.from(caps.userdata_schema),
        declared_events: caps.declared_events,
      });
    }) as grpc.handleUnaryCall<unknown, unknown>,
  });

  const port = await new Promise<number>((resolve, reject) => {
    server.bindAsync(`${cfg.host}:${cfg.port}`, grpc.ServerCredentials.createInsecure(), (err, p) => {
      if (err) return reject(err);
      resolve(p);
    });
  });

  return {
    address: `${cfg.host}:${port}`,
    async shutdown() {
      await new Promise<void>((resolve) => server.tryShutdown(() => resolve()));
    },
  };
}
