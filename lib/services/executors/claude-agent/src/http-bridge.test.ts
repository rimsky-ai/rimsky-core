// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect, beforeEach, afterEach } from "vitest";
import pino from "pino";
import {
  startHttpBridge,
  outcomeToCallbackBody,
  type RunningHttpBridge,
} from "./http-bridge.js";
import {
  startInternalMcpServer,
  type CallbackServerHandle,
} from "./internal-mcp-server.js";
import type { CliHandle, CliRunner } from "./cli-runner.js";
import type { AgentOutcome } from "./agent-run.js";
import { Observability } from "./observability.js";
import { declaredErrorClasses } from "./expected-attributes-schema.js";

const logger = pino({ level: "silent" });

describe("HTTP bridge stub-mode /execute", () => {
  let cb: CallbackServerHandle;
  let bridge: RunningHttpBridge;
  const fakeCli: CliRunner = {
    spawn: async () => {
      throw new Error("should not be called in stub mode");
    },
  };
  const posts: Array<{ url: string; body: unknown }> = [];

  beforeEach(async () => {
    process.env.RIMSKY_EXECUTOR_STUB_MODE = "1";
    posts.length = 0;
    cb = await startInternalMcpServer({ logger });
    bridge = await startHttpBridge({
      host: "127.0.0.1",
      port: 0,
      callback: cb,
      cliRunner: fakeCli,
      silenceTimeoutMsDefault: 5000,
      toolUseTimeoutMsDefault: 0,
      logger,
      postCallback: async (url, body) => {
        posts.push({ url, body });
      },
    });
  });

  afterEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    await bridge.shutdown();
    await cb.close();
  });

  it("returns 202 + ackId and POSTs Complete in the AsyncCallbackBody success oneof", async () => {
    const res = await fetch(`${bridge.address}/execute`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        node_id: "n-1",
        node_type: "stub-agent",
        attributes: { model: "sonnet" },
        attributes_schema: {},
        callback_url: "http://supervisor.invalid/cb",
      }),
    });
    expect(res.status).toBe(202);
    const body = (await res.json()) as { async_ack_id: string };
    expect(body.async_ack_id).toBeTruthy();

    await waitFor(() => posts.length > 0, 2000);
    expect(posts[0]!.url).toBe("http://supervisor.invalid/cb");
    const cb0 = posts[0]!.body as Record<string, unknown>;
    expect(cb0.type).toBeUndefined();
    expect(cb0.kind).toBeUndefined();
    expect(cb0.async_ack_id).toBe(body.async_ack_id);
    const success = cb0.success as Record<string, unknown>;
    expect(success).toBeDefined();
    const delta = success.attributes_delta as Record<string, unknown>;
    expect(delta.stub).toBe(true);
    expect(typeof delta.session_token).toBe("string");
    expect((delta.session_token as string).length).toBeGreaterThan(0);
    expect(success.changed).toBe(true);
  });
});

describe("HTTP bridge /execute rejects a malformed sign-off gate config (no silent ungating)", () => {
  let cb: CallbackServerHandle;
  let bridge: RunningHttpBridge;
  const fakeCli: CliRunner = {
    spawn: async () => {
      throw new Error("spawn must not be reached when cli config is malformed");
    },
  };
  const posts: Array<{ url: string; body: unknown }> = [];

  beforeEach(async () => {
    process.env.RIMSKY_EXECUTOR_STUB_MODE = "1";
    posts.length = 0;
    cb = await startInternalMcpServer({ logger });
    bridge = await startHttpBridge({
      host: "127.0.0.1",
      port: 0,
      callback: cb,
      cliRunner: fakeCli,
      silenceTimeoutMsDefault: 5000,
      toolUseTimeoutMsDefault: 0,
      logger,
      postCallback: async (url, body) => {
        posts.push({ url, body });
      },
    });
  });

  afterEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    await bridge.shutdown();
    await cb.close();
  });

  it("POSTs a terminal agent/attribute_invalid error (not success) when a required_signoffs entry omits public_key", async () => {
    const res = await fetch(`${bridge.address}/execute`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        node_id: "n-gate",
        node_type: "stub-agent",
        dispatch_id: "d-gate",
        attributes: {
          model: "sonnet",
          cli: { required_signoffs: [{ path: "endpoints" }] },
        },
        attributes_schema: {},
        callback_url: "http://supervisor.invalid/cb",
      }),
    });
    expect(res.status).toBe(202);

    await waitFor(() => posts.length > 0, 2000);
    expect(posts).toHaveLength(1);
    const body = posts[0]!.body as Record<string, unknown>;
    expect(body.success).toBeUndefined();
    expect(body.error).toBeDefined();
    const error = body.error as { error_class: string };
    expect(error.error_class).toBe("agent/attribute_invalid");
  });

  it("still resolves to success when the gate config is well-formed (control)", async () => {
    const res = await fetch(`${bridge.address}/execute`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        node_id: "n-ok",
        node_type: "stub-agent",
        dispatch_id: "d-ok",
        attributes: {
          model: "sonnet",
          cli: { required_signoffs: [{ public_key: "PEM", path: "endpoints" }] },
        },
        attributes_schema: {},
        callback_url: "http://supervisor.invalid/cb",
      }),
    });
    expect(res.status).toBe(202);
    await waitFor(() => posts.length > 0, 2000);
    const body = posts[0]!.body as Record<string, unknown>;
    expect(body.success).toBeDefined();
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

describe("HTTP bridge /execute observability ledger", () => {
  let cb: CallbackServerHandle;
  let bridge: RunningHttpBridge;
  let obs: Observability;
  const fakeCli: CliRunner = {
    spawn: async () => {
      throw new Error("should not be called in stub mode");
    },
  };
  const posts: Array<{ url: string; body: unknown }> = [];

  beforeEach(async () => {
    process.env.RIMSKY_EXECUTOR_STUB_MODE = "1";
    posts.length = 0;
    obs = new Observability();
    cb = await startInternalMcpServer({ logger });
    bridge = await startHttpBridge({
      host: "127.0.0.1",
      port: 0,
      callback: cb,
      cliRunner: fakeCli,
      silenceTimeoutMsDefault: 5000,
      toolUseTimeoutMsDefault: 0,
      logger,
      observability: obs,
      postCallback: async (url, body) => {
        posts.push({ url, body });
      },
    });
  });

  afterEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    await bridge.shutdown();
    await cb.close();
  });

  it("records step_started + step_completed against dispatch_id and markCompletes the trace", async () => {
    const dispatchId = "bridge-d1";
    const res = await fetch(`${bridge.address}/execute`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        node_id: "n-obs",
        node_type: "stub-agent",
        dispatch_id: dispatchId,
        attributes: { model: "sonnet" },
        attributes_schema: {},
        callback_url: "http://supervisor.invalid/cb",
      }),
    });
    expect(res.status).toBe(202);
    await waitFor(() => posts.length > 0, 2000);

    const trace = obs.getTrace(dispatchId);
    expect(trace.dispatch_id).toBe(dispatchId);
    const cats = trace.events.map((e) => e.category);
    expect(cats).toContain("step_started");
    expect(cats).toContain("step_completed");
    expect(cats).toContain("trace_complete");
    expect(trace.complete).toBe(true);
  });
});

describe("HTTP bridge /execute threads dispatch-context fields end-to-end", () => {
  let cb: CallbackServerHandle;
  let bridge: RunningHttpBridge;
  const posts: Array<{ url: string; body: unknown }> = [];

  function makeDispatchContextProbeCli(): CliRunner {
    return {
      spawn: async (req) => {
        const exitWaiters: Array<
          (r: { exitCode: number | null; signal: NodeJS.Signals | null }) => void
        > = [];
        let exited = false;
        const resolveExit = (): void => {
          if (exited) return;
          exited = true;
          for (const w of exitWaiters) w({ exitCode: 0, signal: null });
        };
        const url = req.env.RIMSKY_CALLBACK_URL;
        const token = req.env.RIMSKY_CALLBACK_TOKEN;
        setTimeout(() => {
          void (async () => {
            const mod = await import("@modelcontextprotocol/sdk/client/index.js");
            const transportMod = await import(
              "@modelcontextprotocol/sdk/client/streamableHttp.js"
            );
            const transport = new transportMod.StreamableHTTPClientTransport(
              new URL(url),
            );
            const client = new mod.Client({
              name: "rimsky-http-bridge-dispatch-context-probe",
              version: "1.0.0",
            });
            try {
              await client.connect(transport);
              const res = await client.callTool({
                name: "dispatch_context_read",
                arguments: { token },
              });
              const arr = res.content as Array<{ text?: string }>;
              const observed = JSON.parse(arr[0]!.text ?? "null") as Record<
                string,
                unknown
              >;
              await client.callTool({
                name: "report_complete",
                arguments: {
                  token,
                  changed: false,
                  attributes_delta: { observed },
                },
              });
            } finally {
              await client.close().catch(() => {});
            }
          })().catch(() => resolveExit());
        }, 0);
        return {
          pid: 1234,
          onStdout: () => {},
          onStderr: () => {},
          onExit: () => {},
          sendSigterm: () => resolveExit(),
          sendSigkill: () => resolveExit(),
          waitExit: () =>
            exited
              ? Promise.resolve({ exitCode: 0, signal: null })
              : new Promise((resolve) => exitWaiters.push(resolve)),
        };
      },
    };
  }

  beforeEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    posts.length = 0;
    cb = await startInternalMcpServer({ logger });
    bridge = await startHttpBridge({
      host: "127.0.0.1",
      port: 0,
      callback: cb,
      cliRunner: makeDispatchContextProbeCli(),
      silenceTimeoutMsDefault: 60_000,
      toolUseTimeoutMsDefault: 0,
      logger,
      postCallback: async (url, body) => {
        posts.push({ url, body });
      },
    });
  });

  afterEach(async () => {
    await bridge.shutdown();
    await cb.close();
  });

  it("routes run_scope_id + prior_dispatch_id + prior_dispatch_disposition into the agent's dispatch_context_read snapshot", async () => {
    const res = await fetch(`${bridge.address}/execute`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        node_id: "n-ctx",
        node_type: "agent",
        dispatch_id: "d-ctx-2",
        run_scope_id: "rs-ctx-1",
        prior_dispatch_id: "d-ctx-1",
        prior_dispatch_disposition: "PRIOR_RETRY_AFTER_ERROR",
        attributes: { model: "sonnet" },
        attributes_schema: {},
        callback_url: "http://supervisor.invalid/cb",
      }),
    });
    expect(res.status).toBe(202);
    await waitFor(() => posts.length > 0, 5000);
    const body = posts[0]!.body as Record<string, unknown>;
    const success = body.success as Record<string, unknown>;
    expect(success).toBeDefined();
    const delta = success.attributes_delta as Record<string, unknown>;
    const observed = delta.observed as Record<string, unknown>;
    expect(observed).toEqual({
      dispatch_id: "d-ctx-2",
      run_scope_id: "rs-ctx-1",
      prior_dispatch_id: "d-ctx-1",
      prior_dispatch_disposition: "retry_after_error",
    });
  });
});

describe("http-bridge outcomeToCallbackBody park outcome", () => {
  it("encodes sessionToken into scratch on Park (Park does not carry attributes_delta)", () => {
    const resumeAt = new Date("2026-06-04T12:00:00.000Z");
    const outcome: AgentOutcome = {
      kind: "park_requested",
      reason: "snooze",
      reasonNote: "rate-limited; retry after window",
      attributesDelta: null,
      resumeAt,
      sessionToken: "sess-park-1",
    };
    const body = outcomeToCallbackBody(outcome, "ack-park");
    expect(body.async_ack_id).toBe("ack-park");
    expect(["success", "error", "park"].filter((k) => k in body)).toEqual([
      "park",
    ]);
    const park = body.park as Record<string, unknown>;
    expect(park.reason).toBe("snooze");
    expect(park.reason_note).toBe("rate-limited; retry after window");
    expect(park.resume_at).toBe(resumeAt.toISOString());
    expect("session_token" in park).toBe(false);
    expect("payload" in park).toBe(false);
    expect("attributes_delta" in park).toBe(false);
    expect(typeof park.scratch).toBe("string");
    expect(Buffer.from(park.scratch as string, "base64").toString("utf8")).toBe("sess-park-1");
  });

  it("omits resume_at for an indefinite park (resumeAt null) and scratch when sessionToken is empty", () => {
    const outcome: AgentOutcome = {
      kind: "park_requested",
      reason: "await_callback",
      reasonNote: "",
      attributesDelta: null,
      resumeAt: null,
      sessionToken: "",
    };
    const body = outcomeToCallbackBody(outcome, "ack-park-2");
    expect(["success", "error", "park"].filter((k) => k in body)).toEqual([
      "park",
    ]);
    const park = body.park as Record<string, unknown>;
    expect("resume_at" in park).toBe(false);
    expect("attributes_delta" in park).toBe(false);
    expect("payload" in park).toBe(false);
    expect("session_token" in park).toBe(false);
    expect("scratch" in park).toBe(false);
    expect(park.reason).toBe("await_callback");
  });
});

describe("HTTP bridge /execute emits the four declared agent error classes", () => {
  let cb: CallbackServerHandle;

  beforeEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    cb = await startInternalMcpServer({ logger });
  });

  afterEach(async () => {
    await cb.close();
  });

  function fakeCliEmitting(stderr: string, exitCode: number): CliRunner {
    return {
      spawn: async (): Promise<CliHandle> => {
        const stderrCbs: Array<(chunk: string) => void> = [];
        const exitCbs: Array<
          (code: number | null, signal: NodeJS.Signals | null) => void
        > = [];
        const exitWaiters: Array<
          (r: { exitCode: number | null; signal: NodeJS.Signals | null }) => void
        > = [];
        let exited = false;
        let result: { exitCode: number | null; signal: NodeJS.Signals | null } | null =
          null;
        setTimeout(() => {
          for (const c of stderrCbs) c(stderr);
        }, 2);
        setTimeout(() => {
          exited = true;
          result = { exitCode, signal: null };
          for (const c of exitCbs) c(exitCode, null);
          for (const w of exitWaiters) w(result);
        }, 6);
        return {
          pid: 4242,
          onStdout: () => {},
          onStderr: (c) => {
            stderrCbs.push(c);
          },
          onExit: (c) => {
            exitCbs.push(c);
          },
          sendSigterm: () => {},
          sendSigkill: () => {},
          waitExit: () =>
            exited && result
              ? Promise.resolve(result)
              : new Promise((resolve) => exitWaiters.push(resolve)),
        };
      },
    };
  }

  async function dispatchAndCaptureError(opts: {
    stderr: string;
    exitCode: number;
    cli?: Record<string, unknown>;
  }): Promise<Record<string, unknown>> {
    const posts: Array<{ url: string; body: unknown }> = [];
    const bridge = await startHttpBridge({
      host: "127.0.0.1",
      port: 0,
      callback: cb,
      cliRunner: fakeCliEmitting(opts.stderr, opts.exitCode),
      silenceTimeoutMsDefault: 60_000,
      toolUseTimeoutMsDefault: 0,
      logger,
      postCallback: async (url, body) => {
        posts.push({ url, body });
      },
    });
    try {
      const res = await fetch(`${bridge.address}/execute`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          node_id: "n-err",
          node_type: "agent",
          dispatch_id: "d-err",
          attributes: {
            model: "sonnet",
            ...(opts.cli ? { cli: opts.cli } : {}),
          },
          attributes_schema: {},
          callback_url: "http://supervisor.invalid/cb",
        }),
      });
      expect(res.status).toBe(202);
      await waitFor(() => posts.length > 0, 5000);
      return posts[0]!.body as Record<string, unknown>;
    } finally {
      await bridge.shutdown();
    }
  }

  function isDeclared(errorClass: string): boolean {
    return declaredErrorClasses.some((decl) => {
      if (decl === errorClass) return true;
      if (decl.endsWith("/*")) {
        const prefix = decl.slice(0, -1);
        return errorClass.startsWith(prefix);
      }
      return false;
    });
  }

  it("emits agent/context_exceeded, agent/refused, agent/tool_use_failed/<tool>, and agent/rate_limited (handle_rate_limits=false) as terminal Error.error_class", async () => {
    const ctxBody = await dispatchAndCaptureError({
      stderr:
        "API Error: prompt is too long: 215000 tokens > 200000 maximum context window (context_length_exceeded)\n",
      exitCode: 1,
    });
    expect(ctxBody.success).toBeUndefined();
    const ctxErr = ctxBody.error as { error_class: string };
    expect(ctxErr.error_class).toBe("agent/context_exceeded");
    expect(isDeclared(ctxErr.error_class)).toBe(true);

    const refusalBody = await dispatchAndCaptureError({
      stderr:
        "API Error: model declined to respond: this request was refused by the model (refusal)\n",
      exitCode: 1,
    });
    expect(refusalBody.success).toBeUndefined();
    const refusalErr = refusalBody.error as { error_class: string };
    expect(refusalErr.error_class).toBe("agent/refused");
    expect(isDeclared(refusalErr.error_class)).toBe(true);

    const toolBody = await dispatchAndCaptureError({
      stderr:
        'Tool execution failed: tool "Bash" returned a non-recoverable error (tool_use_failed)\n',
      exitCode: 1,
    });
    expect(toolBody.success).toBeUndefined();
    const toolErr = toolBody.error as { error_class: string };
    expect(toolErr.error_class).toBe("agent/tool_use_failed/Bash");
    expect(toolErr.error_class.startsWith("agent/tool_use_failed/")).toBe(true);
    expect(toolErr.error_class.split("/").pop()).toBe("Bash");
    expect(isDeclared(toolErr.error_class)).toBe(true);

    const rlBody = await dispatchAndCaptureError({
      stderr:
        "API Error: 429 rate_limit_error: rate limit exceeded (retry-after: 30)\n",
      exitCode: 1,
      cli: { handle_rate_limits: false },
    });
    expect(rlBody.park).toBeUndefined();
    expect(rlBody.success).toBeUndefined();
    const rlErr = rlBody.error as { error_class: string };
    expect(rlErr.error_class).toBe("agent/rate_limited");
    expect(isDeclared(rlErr.error_class)).toBe(true);
  });
});
