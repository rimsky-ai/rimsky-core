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
      silenceTimeoutMs: 5000,
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
    // @deliberate: AsyncCallbackBody oneof shape the supervisor's parseAsyncCallback
    // requires (success | error | park). The legacy `{type: ...}` / `kind`
    // discriminators are rejected with HTTP 400.
    expect(cb0.type).toBeUndefined();
    expect(cb0.kind).toBeUndefined();
    expect(cb0.async_ack_id).toBe(body.async_ack_id);
    const success = cb0.success as Record<string, unknown>;
    expect(success).toBeDefined();
    // @deliberate: legacy `result` retired in favour of `attributes_delta`.
    expect(success.attributes_delta).toEqual({ stub: true });
    expect(success.changed).toBe(true);
  });
});

// @deliberate: a present-but-malformed cli.required_signoffs (a host typo'd / forgot the
// public_key) must NOT silently degrade to an ungated run. It must terminal-
// ERROR with agent/attribute_invalid — the same fail-loud failure mode as the
// empty-dispatch_id gate path in agent-run.ts — and never resolve to success.
// parseCliConfig throws a CliConfigError in runAndCallback BEFORE runAgent is
// invoked, so the malformed gate config cannot reach terminal success even in
// stub mode (which would otherwise resolve a stub `success`).
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
      silenceTimeoutMs: 5000,
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
          // @deliberate: host forgot/typo'd public_key — only `path` present. Silently
          // dropping this entry would yield an empty (⇒ no) gate and let
          // unsigned output through; the parser must reject it instead.
          cli: { required_signoffs: [{ path: "endpoints" }] },
        },
        attributes_schema: {},
        callback_url: "http://supervisor.invalid/cb",
      }),
    });
    // @deliberate: the /execute handler still acks immediately (async handoff); the
    // terminal verdict rides the callback POST.
    expect(res.status).toBe(202);

    await waitFor(() => posts.length > 0, 2000);
    expect(posts).toHaveLength(1);
    const body = posts[0]!.body as Record<string, unknown>;
    // @deliberate: the dispatch must NOT have reached terminal success.
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
    // @deliberate: a well-formed gate config parses cleanly — stub run reaches success
    // (the gate itself only enforces inside onComplete, not parsed here).
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

// @deliberate: HTTP /execute observability ledger coverage. The bridge keys traces
// by the supervisor-supplied dispatch_id; the dashboard fetches via
// GET /observability/v1/trace/{dispatch_id} so the ledger must end up
// in the complete + step_completed shape after a successful run.
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
      silenceTimeoutMs: 5000,
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

// @deliberate: the HTTP bridge body must also ride the per-dispatch named-event buffer on
// the `events[]` array (the Go supervisor's callback parser reads `events`
// regardless of transport). Payloads are base64-encoded per the proto-JSON
// `bytes` rule.
describe("http-bridge outcomeToCallbackBody named-event surfacing", () => {
  it("populates events[] from the buffer with base64 payloads", () => {
    const payload = Buffer.from(JSON.stringify({ pct: 50 }), "utf8");
    const outcome: AgentOutcome = {
      kind: "complete",
      attributesDelta: { ok: true },
      changed: true,
      changeSummary: "did",
      emittedEvents: [{ name: "progress", payload }],
    };
    const body = outcomeToCallbackBody(outcome, "ack-1");
    expect(body.async_ack_id).toBe("ack-1");
    expect(body.events).toEqual([
      { name: "progress", payload: payload.toString("base64") },
    ]);
    // @deliberate: AsyncCallbackBody oneof shape the supervisor requires (success |
    // error | park) — not the legacy `{type: ...}` discriminator.
    expect(body.success).toBeDefined();
  });

  it("omits events[] entirely when the buffer is empty (no behavior change)", () => {
    const outcome: AgentOutcome = {
      kind: "errored",
      errorClass: "agent/internal_error",
      payload: { error: "boom" },
    };
    const body = outcomeToCallbackBody(outcome, "ack-2");
    expect("events" in body).toBe(false);
    expect(body.error).toBeDefined();
  });
});

// @deliberate: positive coverage for the third AsyncCallbackBody outcome branch. The
// sign-off gate never emits `park` (it only resolves success/errored), so the
// gate e2e can only assert park-ABSENT; the park wire mapping itself
// (outcomeToCallbackBody's park_requested branch) otherwise had no test that a
// park outcome serializes to a body carrying the `park` one-of key. These
// assert exactly that, including the resume_at present/absent split.
describe("http-bridge outcomeToCallbackBody park outcome", () => {
  it("maps park_requested to AsyncCallbackBody.park with exactly one outcome key", () => {
    const payload = Buffer.from(JSON.stringify({ snoozed: true }), "utf8");
    const resumeAt = new Date("2026-06-04T12:00:00.000Z");
    const outcome: AgentOutcome = {
      kind: "park_requested",
      reason: "snooze",
      reasonNote: "rate-limited; retry after window",
      payload,
      resumeAt,
      sessionToken: "sess-park-1",
    };
    const body = outcomeToCallbackBody(outcome, "ack-park");
    expect(body.async_ack_id).toBe("ack-park");
    // @deliberate: exactly one outcome key, and it is `park` — not success/error.
    expect(["success", "error", "park"].filter((k) => k in body)).toEqual([
      "park",
    ]);
    const park = body.park as Record<string, unknown>;
    expect(park.reason).toBe("snooze");
    expect(park.reason_note).toBe("rate-limited; retry after window");
    expect(park.session_token).toBe("sess-park-1");
    // @deliberate: bytes ride as base64 per the proto-JSON convention.
    expect(park.payload).toBe(payload.toString("base64"));
    expect(park.resume_at).toBe(resumeAt.toISOString());
  });

  it("omits resume_at for an indefinite park (resumeAt null)", () => {
    const outcome: AgentOutcome = {
      kind: "park_requested",
      reason: "await_callback",
      reasonNote: "",
      payload: Buffer.alloc(0),
      resumeAt: null,
      sessionToken: "sess-park-2",
    };
    const body = outcomeToCallbackBody(outcome, "ack-park-2");
    expect(["success", "error", "park"].filter((k) => k in body)).toEqual([
      "park",
    ]);
    const park = body.park as Record<string, unknown>;
    expect("resume_at" in park).toBe(false);
    expect(park.reason).toBe("await_callback");
    expect(park.payload).toBe("");
  });
});

// @deliberate: the claude-agent executor declares four hierarchical error
// classes — agent/context_exceeded, agent/refused, agent/tool_use_failed/<tool>,
// and agent/rate_limited — that subscribers prefix-key or exact-key on. A
// subprocess that dies non-zero with a context-exceeded / refusal /
// tool-use-failure stderr must surface one of those declared leaves on the
// terminal error_class, not collapse into the generic
// `agent/subprocess_exit/before_complete` fallback; and a rate-limit stderr
// under `cli.handle_rate_limits=false` likewise must surface
// `agent/rate_limited` (the auto-park branch only fires when handle_rate_limits
// is true). This test drives the REAL HTTP-bridge `/execute` entry point (NO
// stub mode — runAgent's real subprocess-exit classification path runs) with
// four fake-CLI handles, each surfacing one of the four error signatures, and
// asserts the AsyncCallbackBody.error.error_class is the precise declared leaf
// for each. The membership sub-assertion confirms each emitted class is covered
// by the executor's advertised declaredErrorClasses (the `agent/tool_use_failed/*`
// wildcard covers the tool leaf).
describe("HTTP bridge /execute emits the four declared agent error classes", () => {
  let cb: CallbackServerHandle;

  beforeEach(async () => {
    // @deliberate: NOT stub mode — the real runAgent CLI-classification path must run so
    // the subprocess stderr/exit drives the terminal error_class.
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    cb = await startInternalMcpServer({ logger });
  });

  afterEach(async () => {
    await cb.close();
  });

  // @deliberate: a fake CLI handle that emits `stderr` then exits with `exitCode` (non-zero
  // so the exit-0 resume-recovery branch is skipped). No `resume` is provided
  // on the runner, so the recovery path cannot fire — the run resolves purely
  // from the subprocess exit + stderr classification.
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
        // @deliberate: emit the stderr signature, then exit non-zero on the next ticks.
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

  // @deliberate: drive one /execute dispatch end to end against a bridge wired to a fake CLI
  // that surfaces `stderr` + a non-zero exit, returning the single
  // AsyncCallbackBody the bridge POSTs back.
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
      silenceTimeoutMs: 60_000,
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

  // @deliberate: membership check against the hierarchical declared list: an exact entry, or
  // a `prefix/*` wildcard entry whose prefix the emitted class extends.
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
    // @deliberate: 1. Context-window exceeded.
    const ctxBody = await dispatchAndCaptureError({
      stderr:
        "API Error: prompt is too long: 215000 tokens > 200000 maximum context window (context_length_exceeded)\n",
      exitCode: 1,
    });
    expect(ctxBody.success).toBeUndefined();
    const ctxErr = ctxBody.error as { error_class: string };
    expect(ctxErr.error_class).toBe("agent/context_exceeded");
    expect(isDeclared(ctxErr.error_class)).toBe(true);

    // @deliberate: 2. Model refusal.
    const refusalBody = await dispatchAndCaptureError({
      stderr:
        "API Error: model declined to respond: this request was refused by the model (refusal)\n",
      exitCode: 1,
    });
    expect(refusalBody.success).toBeUndefined();
    const refusalErr = refusalBody.error as { error_class: string };
    expect(refusalErr.error_class).toBe("agent/refused");
    expect(isDeclared(refusalErr.error_class)).toBe(true);

    // @deliberate: 3. Tool-invocation failure — the offending tool name rides the
    //    hierarchical leaf.
    const toolBody = await dispatchAndCaptureError({
      stderr:
        'Tool execution failed: tool "Bash" returned a non-recoverable error (tool_use_failed)\n',
      exitCode: 1,
    });
    expect(toolBody.success).toBeUndefined();
    const toolErr = toolBody.error as { error_class: string };
    expect(toolErr.error_class).toBe("agent/tool_use_failed/Bash");
    // @deliberate: the hierarchical leaf must carry the offending tool name as its final
    // segment.
    expect(toolErr.error_class.startsWith("agent/tool_use_failed/")).toBe(true);
    expect(toolErr.error_class.split("/").pop()).toBe("Bash");
    expect(isDeclared(toolErr.error_class)).toBe(true);

    // @deliberate: 4. Rate limit WITH handle_rate_limits=false — the auto-park path is
    //    suppressed, so the rate-limit surfaces as the agent/rate_limited
    //    Error class rather than a Park (and rather than the generic
    //    before_complete leaf).
    const rlBody = await dispatchAndCaptureError({
      stderr:
        "API Error: 429 rate_limit_error: rate limit exceeded (retry-after: 30)\n",
      exitCode: 1,
      cli: { handle_rate_limits: false },
    });
    // @deliberate: it must be an Error, NOT a Park (the suppressed auto-park path).
    expect(rlBody.park).toBeUndefined();
    expect(rlBody.success).toBeUndefined();
    const rlErr = rlBody.error as { error_class: string };
    expect(rlErr.error_class).toBe("agent/rate_limited");
    expect(isDeclared(rlErr.error_class)).toBe(true);
  });
});
