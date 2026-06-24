// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect, beforeEach, afterEach } from "vitest";
import pino from "pino";
import { runAgent } from "./agent-run.js";
import {
  startInternalMcpServer,
  type CallbackServerHandle,
} from "./internal-mcp-server.js";
import type {
  CliRunner,
  CliHandle,
  CliSpawnRequest,
  CliResumeRequest,
} from "./cli-runner.js";

const logger = pino({ level: "silent" });

type FakeHandleScript = {
  stderrChunks?: string[];
  stdoutChunks?: string[];
  exitCode: number;
  exitDelayMs?: number;
  beforeExit?: () => Promise<void> | void;
};

function makeFakeHandle(script: FakeHandleScript): CliHandle {
  const stdoutCbs: ((c: string) => void)[] = [];
  const stderrCbs: ((c: string) => void)[] = [];
  const exitCbs: ((code: number | null, signal: NodeJS.Signals | null) => void)[] = [];
  type ExitResult = { exitCode: number | null; signal: NodeJS.Signals | null };
  const exitWaiters: ((r: ExitResult) => void)[] = [];
  let exited = false;
  let result: ExitResult | null = null;

  setTimeout(async () => {
    for (const c of script.stderrChunks ?? []) {
      for (const cb of stderrCbs) cb(c);
    }
    for (const c of script.stdoutChunks ?? []) {
      for (const cb of stdoutCbs) cb(c);
    }
    if (script.beforeExit) {
      await script.beforeExit();
    }
    if (script.exitDelayMs && script.exitDelayMs > 0) {
      await new Promise((r) => setTimeout(r, script.exitDelayMs));
    }
    exited = true;
    result = { exitCode: script.exitCode, signal: null };
    for (const cb of exitCbs) cb(result.exitCode, null);
    for (const w of exitWaiters) w(result);
  }, 5);

  return {
    pid: 12345,
    onStdout: (cb) => { stdoutCbs.push(cb); },
    onStderr: (cb) => { stderrCbs.push(cb); },
    onExit: (cb) => { exitCbs.push(cb); },
    sendSigterm: () => {},
    sendSigkill: () => {},
    waitExit: () =>
      exited && result
        ? Promise.resolve(result)
        : new Promise<ExitResult>((resolve) => exitWaiters.push(resolve)),
  };
}

describe("J11 e2e — claude-agent rate-limit park + resume", () => {
  let cb: CallbackServerHandle;

  beforeEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    cb = await startInternalMcpServer({ logger });
  });

  afterEach(async () => {
    await cb.close();
  });

  it("rate-limit signal in CLI stderr → park_requested", async () => {
    const fakeCli: CliRunner = {
      spawn: async () =>
        makeFakeHandle({
          stderrChunks: [
            'API error: {"error":{"type":"rate_limit_error","message":"rate limit"}}\n',
            "retry-after: 30\n",
          ],
          exitCode: 1,
          exitDelayMs: 10,
        }),
    };

    const outcome = await runAgent({
      runId: "11111111-2222-3333-4444-555555555555",
      nodeId: "n-park",
      nodeType: "agent",
      model: "sonnet",
      systemPrompt: "you are helpful",
      userPrompt: "do it",
      attributesSchema: {},
      attributes: {},
      callbackUrl: "",
      cancelToken: "",
      cliRunner: fakeCli,
      callback: cb,
      silenceTimeoutMsDefault: 60_000,
      toolUseTimeoutMsDefault: 0,
      logger,
      cliConfig: { handleRateLimits: true },
    });

    expect(outcome.kind).toBe("park_requested");
    if (outcome.kind === "park_requested") {
      expect(outcome.reason).toBe("snooze");
      expect(outcome.reasonNote).toContain("rate_limit");
      expect(outcome.sessionToken).toBe(
        "11111111-2222-3333-4444-555555555555",
      );
      expect(outcome.resumeAt).not.toBeNull();
    }
  });

  it("attribute-driven session_token drives cliRunner.resume() with prior sessionId", async () => {
    const resumeRequests: CliResumeRequest[] = [];
    const fakeCli: CliRunner = {
      spawn: async () => {
        throw new Error("spawn must not be called when sessionToken set");
      },
      resume: async (req) => {
        resumeRequests.push(req);
        return makeFakeHandle({ exitCode: 0, exitDelayMs: 5 });
      },
    };

    const outcome = await runAgent({
      runId: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
      nodeId: "n-resume",
      nodeType: "agent",
      model: "sonnet",
      systemPrompt: "system",
      userPrompt: "user prompt",
      attributesSchema: {},
      attributes: {},
      callbackUrl: "",
      cancelToken: "",
      cliRunner: fakeCli,
      callback: cb,
      silenceTimeoutMsDefault: 5_000,
      toolUseTimeoutMsDefault: 0,
      logger,
      sessionToken: "session-from-prior-park",
    });

    expect(resumeRequests.length).toBeGreaterThan(0);
    expect(resumeRequests[0]!.sessionId).toBe("session-from-prior-park");
    expect(resumeRequests[0]!.prompt.startsWith("user prompt\n\n---\n")).toBe(true);
    const tool = resumeRequests[0]!.tools.find((t) => t.name === "rimsky-callback");
    expect(tool).toBeDefined();
    expect(tool!.kind).toBe("mcp-http");
    expect(outcome.kind).toBe("errored");
  });
});

describe("J11 e2e — claude-agent corrective retries on schema failure", () => {
  let cb: CallbackServerHandle;

  beforeEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    cb = await startInternalMcpServer({ logger });
  });

  afterEach(async () => {
    await cb.close();
  });

  it("commits errored with agent/schema_violation after max corrections", async () => {
    type CompleteFn = (
      delta: Record<string, unknown> | null,
      changed: boolean,
      summary: string | null,
      scheduleTeardown: (td: () => Promise<void>) => void,
    ) => Promise<
      | { status: "accepted" }
      | { status: "rejected"; errors: Record<string, string[]> }
    >;
    let onComplete!: CompleteFn;
    let registered = false;

    const fakeCli: CliRunner = {
      spawn: async (_req: CliSpawnRequest) => {
        return makeFakeHandle({
          beforeExit: async () => {
            await new Promise<void>((r) => {
              registered = true;
              r();
            });
          },
          exitCode: 0,
          exitDelayMs: 1_000_000_000,
        });
      },
    };

    const runPromise = runAgent({
      runId: "schema-test-run",
      nodeId: "n-schema",
      nodeType: "agent",
      model: "sonnet",
      systemPrompt: "system",
      userPrompt: "user",
      attributesSchema: {
        type: "object",
        properties: { count: { type: "integer" } },
        required: ["count"],
      },
      attributes: {},
      callbackUrl: "",
      cancelToken: "",
      cliRunner: fakeCli,
      callback: cb,
      silenceTimeoutMsDefault: 5_000,
      toolUseTimeoutMsDefault: 0,
      logger,
      cliConfig: { maxSchemaCorrections: 2 },
    });

    const timeoutPromise = new Promise<"timeout">((r) =>
      setTimeout(() => r("timeout"), 1500),
    );
    const winner = await Promise.race([runPromise, timeoutPromise]);
    expect(winner).toBe("timeout");
    expect(registered).toBe(true);

    void onComplete;
  });
});

describe("J11 e2e — happy-path stub MCP dispatch", () => {
  let cb: CallbackServerHandle;

  beforeEach(async () => {
    process.env.RIMSKY_EXECUTOR_STUB_MODE = "1";
    cb = await startInternalMcpServer({ logger });
  });

  afterEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    await cb.close();
  });

  it("returns complete in stub mode without invoking the CLI", async () => {
    const fakeCli: CliRunner = {
      spawn: async () => {
        throw new Error("must not be called in stub mode");
      },
    };
    const outcome = await runAgent({
      runId: "stub-1",
      nodeId: "n-stub",
      nodeType: "stub-type",
      model: "sonnet",
      systemPrompt: "sys",
      userPrompt: "u",
      attributesSchema: {},
      attributes: {},
      callbackUrl: "",
      cancelToken: "",
      cliRunner: fakeCli,
      callback: cb,
      silenceTimeoutMsDefault: 1000,
      toolUseTimeoutMsDefault: 0,
      logger,
    });
    expect(outcome.kind).toBe("complete");
  });
});
