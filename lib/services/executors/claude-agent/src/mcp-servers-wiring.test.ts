// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// @deliberate: pass 3 of the sign-off-gate plan: host-declared `cli.mcp_servers` must
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

/**
 * Minimal fake handle that exits 0 after a short delay so runAgent's
 * exit-watcher resolves the dispatch quickly. The fake-CLI never calls
 * any callback, so runAgent falls through to the clean-exit recovery /
 * before_complete path — we don't care about the outcome here, only the
 * captured spawn request.
 */
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

    // @deliberate: the internal rimsky-callback server is always present.
    const callbackTool = req.tools.find((t) => t.name === "rimsky-callback");
    expect(callbackTool).toBeDefined();
    expect(callbackTool!.kind).toBe("mcp-http");

    // @deliberate: the host-declared validator server is appended in addition.
    const validatorTool = req.tools.find((t) => t.name === "validator");
    expect(validatorTool).toBeDefined();
    expect(validatorTool!.kind).toBe("mcp-http");
    expect(validatorTool!.url).toBe("https://validator/mcp");

    // @deliberate: with no explicit per-server allowed_tools, the bare server-prefix
    // entry `mcp__validator` auto-allows ALL of that server's tools.
    expect(req.allowedTools).toBeDefined();
    expect(req.allowedTools).toContain("mcp__validator");
  });

  // @deliberate: the executor is started with a startup MCP-server catalog
  // and an `allow_inline=false` policy. A node references a catalog entry
  // via `{ ref: <name> }` rather than declaring an inline `{name,url}`
  // server. Two observable behaviors the current tree does NOT deliver:
  //
  //   (1) A `{ ref: "shape-validator" }` reference must resolve against the
  //       catalog to its STDIO-transport definition. The captured spawn's
  //       `--mcp-config` entry for `shape-validator` must therefore be a
  //       stdio leaf carrying the catalog's `command`/`args` — NOT an
  //       `mcp-http` entry — and its tools must be folded into
  //       `--allowedTools`. Today `CliToolConfig` has only the `mcp-http`
  //       leaf and `parseMcpServers` has no `{ref:}` branch, so a `{ref:}`
  //       entry is rejected as a malformed inline server (missing name/url),
  //       the dispatch errors out, and no spawn is captured.
  //
  //   (2) An INLINE server (`{name,url}` with no ref), under
  //       `allow_inline=false`, must be REJECTED at dispatch with a config
  //       error citing `allow_inline`. Today there is no `allow_inline`
  //       policy at all, so inline servers are always accepted and the
  //       dispatch is never rejected.
  //
  // The catalog + policy thread into `AgentRunOptions` (the carrier for
  // `cliConfig`) as `mcpCatalog` + `mcpAllowInline`, mirroring how the
  // startup catalog/policy reach a real dispatch.
  it("resolves a stdio catalog ref to a stdio --mcp-config entry and rejects inline servers when allow_inline is false", async () => {
    const catalog = {
      "shape-validator": {
        transport: "stdio" as const,
        command: "shape-validator",
        args: ["--mode", "strict"],
      },
    };

    // @deliberate: (1) A `{ ref: "shape-validator" }` reference resolves to the catalog's
    //     stdio definition. Capture the spawn and inspect the resolved tool.
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

    // @deliberate: the resolved tool is the catalog's stdio definition, NOT an http leaf.
    const resolved = req.tools.find((t) => t.name === "shape-validator");
    expect(resolved).toBeDefined();
    // @deliberate: the stdio leaf carries the catalog's command/args; the http leaf would
    // be `kind: "mcp-http"` with a `url` — that is exactly what must NOT
    // happen for a stdio catalog entry.
    expect(resolved!.kind).not.toBe("mcp-http");
    const stdioTool = resolved! as unknown as {
      kind: string;
      command?: string;
      args?: string[];
    };
    expect(stdioTool.kind).toBe("mcp-stdio");
    expect(stdioTool.command).toBe("shape-validator");
    expect(stdioTool.args).toEqual(["--mode", "strict"]);

    // @deliberate: the resolved server's tools are auto-allowed (bare server-prefix entry,
    // no per-server allowed_tools declared on the catalog ref).
    expect(req.allowedTools).toBeDefined();
    expect(req.allowedTools).toContain("mcp__shape-validator");

    // @deliberate: (2) An inline server (name+url, no ref) under allow_inline=false is
    //     rejected at dispatch with a config error citing `allow_inline`.
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

    // @deliberate: the inline server must be rejected BEFORE the CLI is spawned, and the
    // rejection must surface as a config error (agent/attribute_invalid)
    // whose reason names the `allow_inline` policy.
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
    // @deliberate: explicit per-server allowed_tools narrow to the fully-qualified names.
    expect(req.allowedTools).toContain("mcp__validator__sign");
    expect(req.allowedTools).toContain("mcp__validator__info");
    // @deliberate: ...and do NOT add the broad server-prefix entry.
    expect(req.allowedTools).not.toContain("mcp__validator");
  });
});
