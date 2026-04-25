import { describe, it, expect, beforeEach, afterEach } from "vitest";
import * as grpc from "@grpc/grpc-js";
import pino from "pino";
import * as http from "node:http";
import type { AddressInfo } from "node:net";
import { loadNodeExecutorProto } from "./proto-loader.js";
import { startGrpcServer, type RunningServer } from "./server.js";
import {
  startInternalMcpServer,
  type CallbackServerHandle,
} from "./internal-mcp-server.js";
import type { CliRunner } from "./cli-runner.js";

const logger = pino({ level: "silent" });

interface ExecuteEvent {
  heartbeat?: { timestamp_ms: number; note: string };
  async_accepted?: { async_ack_id: string; expected_completion_ms: number };
  complete?: { result: unknown; changed: boolean; change_summary: string };
  blocked?: { reason: string };
  errored?: { error_class: string };
}

describe("gRPC server stub-mode Execute end-to-end", () => {
  let cb: CallbackServerHandle;
  let srv: RunningServer;
  const fakeCli: CliRunner = {
    spawn: async () => {
      throw new Error("should not be called in stub mode");
    },
  };

  // Capture calls made via the supervisor callback URL (mocked).
  const callbackPosts: Array<{ url: string; body: unknown }> = [];
  const fakeCallbackUrl = "http://supervisor.invalid/rimsky/callback";

  beforeEach(async () => {
    process.env.RIMSKY_EXECUTOR_STUB_MODE = "1";
    callbackPosts.length = 0;
    cb = await startInternalMcpServer({ logger });
    srv = await startGrpcServer({
      host: "127.0.0.1",
      port: 0,
      callback: cb,
      cliRunner: fakeCli,
      silenceTimeoutMs: 5000,
      logger,
      postCallback: async (url, body) => {
        callbackPosts.push({ url, body });
      },
    });
  });

  afterEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    await srv.shutdown();
    await cb.close();
  });

  it("emits Heartbeat + AsyncAccepted, then POSTs Complete outcome", async () => {
    const pkg = loadNodeExecutorProto();
    const Client = pkg.rimsky.v1.NodeExecutor as unknown as new (
      addr: string,
      creds: grpc.ChannelCredentials,
    ) => grpc.Client;
    const client = new Client(srv.address, grpc.credentials.createInsecure());
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const call = (client as any).Execute({
      node_id: "n-1",
      instance_id: "i-1",
      node_type: "stub-agent",
      userdata: {
        fields: {
          model: { string_value: "sonnet" },
          system_prompt: { string_value: "sys" },
          user_prompt_template: { string_value: "usr" },
        },
      },
      instance_params: { fields: {} },
      callback_url: fakeCallbackUrl,
    });

    const events: ExecuteEvent[] = [];
    await new Promise<void>((resolve, reject) => {
      call.on("data", (ev: ExecuteEvent) => events.push(ev));
      call.on("error", reject);
      call.on("end", resolve);
    });

    expect(events.length).toBeGreaterThanOrEqual(2);
    expect(events[0]!.heartbeat).toBeDefined();
    const terminal = events[events.length - 1]!;
    expect(terminal.async_accepted).toBeDefined();
    const ackId = terminal.async_accepted!.async_ack_id;
    expect(ackId).toBeTruthy();

    // Wait for the background agent run + callback POST. Stub is ~50ms.
    await waitFor(() => callbackPosts.length > 0, 2000);
    expect(callbackPosts).toHaveLength(1);
    // Executor appends /v1/callback/{ackID} to the supervisor-provided base.
    expect(callbackPosts[0]!.url).toBe(
      `${fakeCallbackUrl}/v1/callback/${encodeURIComponent(ackId)}`,
    );
    const body = callbackPosts[0]!.body as Record<string, unknown>;
    expect(body.type).toBe("complete");
    expect(body.result).toEqual({ stub: true });
    expect(body.changed).toBe(true);

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (client as any).close?.();
  });
});

async function waitFor(
  fn: () => boolean,
  timeoutMs: number,
): Promise<void> {
  const start = Date.now();
  while (!fn()) {
    if (Date.now() - start > timeoutMs) throw new Error("waitFor: timed out");
    await new Promise((r) => setTimeout(r, 20));
  }
}

// End-to-end coverage of the TS executor -> Go supervisor callback protocol.
// Rather than spin up a full Go supervisor here (out of scope for TS tests),
// we stand up a plain HTTP server that mimics the supervisor's chi routing:
//   POST /v1/callback/{ackID}  -> captures ackID from path + body
// This guarantees the executor's URL shape and body format line up with the
// supervisor's handler in core/supervisor/callback.go.
describe("gRPC executor -> supervisor callback (protocol shape)", () => {
  let cb: CallbackServerHandle;
  let srv: RunningServer;
  let supervisorLike: http.Server;
  let supervisorBase: string;
  const received: Array<{ path: string; ackId: string; body: any }> = [];

  const fakeCli: CliRunner = {
    spawn: async () => {
      throw new Error("should not be called in stub mode");
    },
  };

  beforeEach(async () => {
    process.env.RIMSKY_EXECUTOR_STUB_MODE = "1";
    received.length = 0;

    // Minimal supervisor-like HTTP server that matches the Go chi route
    // `POST /v1/callback/{ackID}`. This is the end-to-end assertion.
    supervisorLike = http.createServer((req, res) => {
      const match = /^\/v1\/callback\/([^/]+)$/.exec(req.url ?? "");
      if (!match || req.method !== "POST") {
        res.statusCode = 404;
        res.end(JSON.stringify({ error: "not found" }));
        return;
      }
      const ackId = decodeURIComponent(match[1]!);
      const chunks: Buffer[] = [];
      req.on("data", (c) => chunks.push(c));
      req.on("end", () => {
        let parsed: unknown;
        try {
          parsed = JSON.parse(Buffer.concat(chunks).toString("utf8"));
        } catch {
          res.statusCode = 400;
          res.end(JSON.stringify({ error: "invalid json" }));
          return;
        }
        received.push({ path: req.url ?? "", ackId, body: parsed });
        res.statusCode = 200;
        res.setHeader("content-type", "application/json");
        res.end(JSON.stringify({ status: "accepted" }));
      });
    });
    await new Promise<void>((resolve) =>
      supervisorLike.listen(0, "127.0.0.1", () => resolve()),
    );
    const addr = supervisorLike.address() as AddressInfo;
    supervisorBase = `http://127.0.0.1:${addr.port}`;

    cb = await startInternalMcpServer({ logger });
    srv = await startGrpcServer({
      host: "127.0.0.1",
      port: 0,
      callback: cb,
      cliRunner: fakeCli,
      silenceTimeoutMs: 5000,
      logger,
      // Use the real defaultPostCallback (no postCallback override) so we
      // actually exercise the network round-trip.
    });
  });

  afterEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    await srv.shutdown();
    await cb.close();
    await new Promise<void>((resolve) => supervisorLike.close(() => resolve()));
  });

  it("POSTs to /v1/callback/{ackID} with a body keyed by `type`", async () => {
    const pkg = loadNodeExecutorProto();
    const Client = pkg.rimsky.v1.NodeExecutor as unknown as new (
      addr: string,
      creds: grpc.ChannelCredentials,
    ) => grpc.Client;
    const client = new Client(srv.address, grpc.credentials.createInsecure());
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const call = (client as any).Execute({
      node_id: "n-e2e",
      instance_id: "i-e2e",
      node_type: "stub-agent",
      userdata: {
        fields: {
          model: { string_value: "sonnet" },
          system_prompt: { string_value: "sys" },
          user_prompt_template: { string_value: "usr" },
        },
      },
      instance_params: { fields: {} },
      callback_url: supervisorBase,
    });

    interface ExecuteEventLocal {
      heartbeat?: { timestamp_ms: number; note: string };
      async_accepted?: { async_ack_id: string; expected_completion_ms: number };
    }
    const events: ExecuteEventLocal[] = [];
    await new Promise<void>((resolve, reject) => {
      call.on("data", (ev: ExecuteEventLocal) => events.push(ev));
      call.on("error", reject);
      call.on("end", resolve);
    });
    const terminal = events[events.length - 1]!;
    const ackId = terminal.async_accepted!.async_ack_id;
    expect(ackId).toBeTruthy();

    await waitFor(() => received.length > 0, 3000);
    expect(received).toHaveLength(1);
    expect(received[0]!.path).toBe(
      `/v1/callback/${encodeURIComponent(ackId)}`,
    );
    expect(received[0]!.ackId).toBe(ackId);
    expect(received[0]!.body.type).toBe("complete");
    // Ensure we did NOT use the legacy `kind` key.
    expect(received[0]!.body.kind).toBeUndefined();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (client as any).close?.();
  });
});
