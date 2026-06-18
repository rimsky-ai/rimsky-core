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
} from "./cli-runner.js";

const logger = pino({ level: "silent" });

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

    const callbackTool = req.tools.find((t) => t.name === "rimsky-callback");
    expect(callbackTool).toBeDefined();
    expect(callbackTool!.kind).toBe("mcp-http");

    const validatorTool = req.tools.find((t) => t.name === "validator");
    expect(validatorTool).toBeDefined();
    expect(validatorTool!.kind).toBe("mcp-http");
    expect(validatorTool!.url).toBe("https://validator/mcp");

    expect(req.allowedTools).toBeDefined();
    expect(req.allowedTools).toContain("mcp__validator");
  });

  it("resolves a stdio catalog ref to a stdio --mcp-config entry and rejects inline servers when allow_inline is false", async () => {
    const catalog = {
      "shape-validator": {
        transport: "stdio" as const,
        command: "shape-validator",
        args: ["--mode", "strict"],
      },
    };

    let captured: CliSpawnRequest | null = null;
    const fakeCli: CliRunner = {
      spawn: async (req: CliSpawnRequest) => {
        captured = req;
        return makeQuickExitHandle();
      },
    };

    await runAgent({
      runId: "wiring-catalog-ref",
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
      mcpCatalog: catalog,
      mcpAllowInline: false,
      cliConfig: {
        mcpServers: [{ ref: "shape-validator" }],
      },
    });

    expect(captured).not.toBeNull();
    const req = captured! as CliSpawnRequest;

    const resolved = req.tools.find((t) => t.name === "shape-validator");
    expect(resolved).toBeDefined();
    expect(resolved!.kind).not.toBe("mcp-http");
    const stdioTool = resolved! as unknown as {
      kind: string;
      command?: string;
      args?: string[];
    };
    expect(stdioTool.kind).toBe("mcp-stdio");
    expect(stdioTool.command).toBe("shape-validator");
    expect(stdioTool.args).toEqual(["--mode", "strict"]);

    expect(req.allowedTools).toBeDefined();
    expect(req.allowedTools).toContain("mcp__shape-validator");

    let inlineSpawned = false;
    const inlineCli: CliRunner = {
      spawn: async () => {
        inlineSpawned = true;
        return makeQuickExitHandle();
      },
    };

    const outcome = await runAgent({
      runId: "wiring-catalog-inline",
      nodeId: "n-wiring",
      nodeType: "agent",
      model: "sonnet",
      systemPrompt: "system",
      userPrompt: "go",
      attributesSchema: {},
      attributes: {},
      callbackUrl: "",
      cancelToken: "",
      cliRunner: inlineCli,
      callback: cb,
      silenceTimeoutMs: 5_000,
      logger,
      mcpCatalog: catalog,
      mcpAllowInline: false,
      cliConfig: {
        mcpServers: [{ name: "x", url: "http://x" }],
      },
    });

    expect(inlineSpawned).toBe(false);
    expect(outcome.kind).toBe("errored");
    if (outcome.kind === "errored") {
      expect(outcome.errorClass).toBe("agent/attribute_invalid");
      const reason = JSON.stringify(outcome.payload ?? {});
      expect(reason).toContain("allow_inline");
    }
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
    expect(req.allowedTools).toContain("mcp__validator__sign");
    expect(req.allowedTools).toContain("mcp__validator__info");
    expect(req.allowedTools).not.toContain("mcp__validator");
  });
});
