// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// Pass 3 of the sign-off-gate plan: host-declared `cli.mcp_servers` must
// reach the spawned CLI's `--mcp-config` (the per-spawn `tools` list) and
// have all their tools auto-allowed into `--allowedTools` (the per-spawn
// `allowedTools` list). The gate (later pass) depends on the agent being
// able to actually dial the validator servers — a URL only mentioned in a
// prompt is unreachable; it must be wired via `--mcp-config`.
//
// We drive the real `runAgent` with a fake `CliRunner.spawn` that records
// the `CliSpawnRequest` and returns a handle that exits 0 quickly (the
// fake-CLI technique from `src/lifecycle.e2e.test.ts::makeFakeHandle`).

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
} from "./cli-runner.js";

const logger = pino({ level: "silent" });

// Minimal fake handle that exits 0 after a short delay so runAgent's
// exit-watcher resolves the dispatch quickly. The fake-CLI never calls
// any callback, so runAgent falls through to the clean-exit recovery /
// before_complete path — we don't care about the outcome here, only the
// captured spawn request.
function makeQuickExitHandle(): CliHandle {
  const exitCbs: ((code: number | null, signal: NodeJS.Signals | null) => void)[] = [];
  type ExitResult = { exitCode: number | null; signal: NodeJS.Signals | null };
  const exitWaiters: ((r: ExitResult) => void)[] = [];
  let exited = false;
  let result: ExitResult | null = null;

  setTimeout(() => {
    exited = true;
    result = { exitCode: 0, signal: null };
    for (const cb of exitCbs) cb(result.exitCode, null);
    for (const w of exitWaiters) w(result);
  }, 5);

  return {
    pid: 4242,
    onStdout: () => {},
    onStderr: () => {},
    onExit: (cb) => { exitCbs.push(cb); },
    sendSigterm: () => {},
    sendSigkill: () => {},
    waitExit: () =>
      exited && result
        ? Promise.resolve(result)
        : new Promise<ExitResult>((resolve) => exitWaiters.push(resolve)),
  };
}

describe("host MCP servers wired into the spawned CLI", () => {
  let cb: CallbackServerHandle;

  beforeEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    cb = await startInternalMcpServer({ logger });
  });

  afterEach(async () => {
    await cb.close();
  });

  it("appends host mcp_servers to the spawn tools and auto-allows them", async () => {
    let captured: CliSpawnRequest | null = null;
    const fakeCli: CliRunner = {
      spawn: async (req: CliSpawnRequest) => {
        captured = req;
        return makeQuickExitHandle();
      },
    };

    await runAgent({
      runId: "wiring-run-1",
      nodeId: "n-wiring",
      nodeType: "agent",
      model: "sonnet",
      systemPrompt: "system",
      userPrompt: "go",
      attributesSchema: {},
      attributes: {},
      callbackUrl: "",
      cancelToken: "",
      cliRunner: fakeCli,
      callback: cb,
      silenceTimeoutMs: 5_000,
      logger,
      cliConfig: {
        mcpServers: [{ name: "validator", url: "https://validator/mcp" }],
      },
    });

    expect(captured).not.toBeNull();
    const req = captured! as CliSpawnRequest;

    // The internal rimsky-callback server is always present.
    const callbackTool = req.tools.find((t) => t.name === "rimsky-callback");
    expect(callbackTool).toBeDefined();
    expect(callbackTool!.kind).toBe("mcp-http");

    // The host-declared validator server is appended in addition.
    const validatorTool = req.tools.find((t) => t.name === "validator");
    expect(validatorTool).toBeDefined();
    expect(validatorTool!.kind).toBe("mcp-http");
    expect(validatorTool!.url).toBe("https://validator/mcp");

    // With no explicit per-server allowed_tools, the bare server-prefix
    // entry `mcp__validator` auto-allows ALL of that server's tools.
    expect(req.allowedTools).toBeDefined();
    expect(req.allowedTools).toContain("mcp__validator");
  });

  it("narrows the allowlist when a host server declares allowed_tools", async () => {
    let captured: CliSpawnRequest | null = null;
    const fakeCli: CliRunner = {
      spawn: async (req: CliSpawnRequest) => {
        captured = req;
        return makeQuickExitHandle();
      },
    };

    await runAgent({
      runId: "wiring-run-2",
      nodeId: "n-wiring",
      nodeType: "agent",
      model: "sonnet",
      systemPrompt: "system",
      userPrompt: "go",
      attributesSchema: {},
      attributes: {},
      callbackUrl: "",
      cancelToken: "",
      cliRunner: fakeCli,
      callback: cb,
      silenceTimeoutMs: 5_000,
      logger,
      cliConfig: {
        mcpServers: [
          { name: "validator", url: "https://validator/mcp", allowedTools: ["sign", "info"] },
        ],
      },
    });

    expect(captured).not.toBeNull();
    const req = captured! as CliSpawnRequest;

    expect(req.tools.find((t) => t.name === "validator")).toBeDefined();
    expect(req.allowedTools).toBeDefined();
    // Explicit per-server allowed_tools narrow to the fully-qualified names.
    expect(req.allowedTools).toContain("mcp__validator__sign");
    expect(req.allowedTools).toContain("mcp__validator__info");
    // ...and do NOT add the broad server-prefix entry.
    expect(req.allowedTools).not.toContain("mcp__validator");
  });
});
