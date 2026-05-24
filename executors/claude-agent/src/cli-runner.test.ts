// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  buildClaudeCliArgs,
  buildClaudeCliResumeArgs,
  type CliResumeRequest,
  type CliSpawnRequest,
} from "./cli-runner.js";

const PATHS = {
  systemPromptPath: "/tmp/sys.md",
  mcpConfigPath: "/tmp/mcp.json",
} as const;

function baseReq(overrides: Partial<CliSpawnRequest> = {}): CliSpawnRequest {
  return {
    model: "claude-sonnet-4-6",
    systemPrompt: "S",
    userPrompt: "U",
    tools: [],
    env: {},
    ...overrides,
  };
}

describe("buildClaudeCliArgs", () => {
  const originalEnv = process.env.RIMSKY_DISPATCH_MAX_USD;
  beforeEach(() => {
    delete process.env.RIMSKY_DISPATCH_MAX_USD;
  });
  afterEach(() => {
    if (originalEnv === undefined) delete process.env.RIMSKY_DISPATCH_MAX_USD;
    else process.env.RIMSKY_DISPATCH_MAX_USD = originalEnv;
  });

  it("emits the fixed core with current defaults when no cli config is supplied", () => {
    const args = buildClaudeCliArgs(baseReq(), PATHS);
    expect(args).toEqual([
      "--print",
      "--output-format",
      "stream-json",
      "--verbose",
      "--model",
      "claude-sonnet-4-6",
      "--permission-mode",
      "bypassPermissions",
      "--system-prompt-file",
      "/tmp/sys.md",
      "--mcp-config",
      "/tmp/mcp.json",
      "-p",
      "U",
    ]);
  });

  it("splices --bare when bare=true", () => {
    const args = buildClaudeCliArgs(baseReq({ bare: true }), PATHS);
    expect(args).toContain("--bare");
    // Position: between --permission-mode block and --allowedTools block.
    const i = args.indexOf("--bare");
    expect(args[i - 1]).toBe("bypassPermissions");
  });

  it("omits --bare when bare=false or absent", () => {
    expect(buildClaudeCliArgs(baseReq({ bare: false }), PATHS)).not.toContain("--bare");
    expect(buildClaudeCliArgs(baseReq(), PATHS)).not.toContain("--bare");
  });

  it("uses the supplied permissionMode instead of the default", () => {
    const args = buildClaudeCliArgs(baseReq({ permissionMode: "acceptEdits" }), PATHS);
    const i = args.indexOf("--permission-mode");
    expect(args[i + 1]).toBe("acceptEdits");
  });

  it("joins allowedTools and disallowedTools with spaces", () => {
    const args = buildClaudeCliArgs(
      baseReq({
        allowedTools: ["Read", "Edit", "mcp__rimsky-callback__report_complete"],
        disallowedTools: ["Bash"],
      }),
      PATHS,
    );
    const aIdx = args.indexOf("--allowedTools");
    expect(aIdx).toBeGreaterThan(-1);
    expect(args[aIdx + 1]).toBe("Read Edit mcp__rimsky-callback__report_complete");
    const dIdx = args.indexOf("--disallowedTools");
    expect(dIdx).toBeGreaterThan(-1);
    expect(args[dIdx + 1]).toBe("Bash");
  });

  it("omits --allowedTools / --disallowedTools when arrays are empty", () => {
    const args = buildClaudeCliArgs(
      baseReq({ allowedTools: [], disallowedTools: [] }),
      PATHS,
    );
    expect(args).not.toContain("--allowedTools");
    expect(args).not.toContain("--disallowedTools");
  });

  it("forwards each addDirs entry as a separate token after --add-dir", () => {
    // The CLI's `--add-dir <directories...>` is variadic; passing each path as a
    // separate argv element matches the documented shape and avoids whitespace-
    // in-paths bugs that a joined-string approach would silently introduce.
    const args = buildClaudeCliArgs(
      baseReq({ addDirs: ["../specs", "../guidance"] }),
      PATHS,
    );
    const i = args.indexOf("--add-dir");
    expect(i).toBeGreaterThan(-1);
    expect(args[i + 1]).toBe("../specs");
    expect(args[i + 2]).toBe("../guidance");
  });

  it("prefers req.maxBudgetUsd over RIMSKY_DISPATCH_MAX_USD env var", () => {
    process.env.RIMSKY_DISPATCH_MAX_USD = "10.00";
    const args = buildClaudeCliArgs(baseReq({ maxBudgetUsd: "0.50" }), PATHS);
    const i = args.indexOf("--max-budget-usd");
    expect(i).toBeGreaterThan(-1);
    expect(args[i + 1]).toBe("0.50");
  });

  it("falls back to RIMSKY_DISPATCH_MAX_USD when req.maxBudgetUsd is unset", () => {
    process.env.RIMSKY_DISPATCH_MAX_USD = "10.00";
    const args = buildClaudeCliArgs(baseReq(), PATHS);
    const i = args.indexOf("--max-budget-usd");
    expect(i).toBeGreaterThan(-1);
    expect(args[i + 1]).toBe("10.00");
  });

  it("omits --max-budget-usd when neither source is set", () => {
    const args = buildClaudeCliArgs(baseReq(), PATHS);
    expect(args).not.toContain("--max-budget-usd");
  });

  it("emits --session-id when sessionId is supplied", () => {
    const args = buildClaudeCliArgs(
      baseReq({ sessionId: "550e8400-e29b-41d4-a716-446655440000" }),
      PATHS,
    );
    const i = args.indexOf("--session-id");
    expect(i).toBeGreaterThan(-1);
    expect(args[i + 1]).toBe("550e8400-e29b-41d4-a716-446655440000");
  });

  it("omits --session-id when sessionId is unset", () => {
    const args = buildClaudeCliArgs(baseReq(), PATHS);
    expect(args).not.toContain("--session-id");
  });

  it("emits --mcp-config on resume (regression: bug 2 — resume() dropping mcp-config wedged sessions with 'MCP server not connected')", () => {
    // The bug: prior versions of resume() omitted --mcp-config on the
    // assumption that --resume would restore it from session state.
    // It does not; --mcp-config is process-local runtime config. The
    // resumed subprocess had no rimsky-callback MCP server to dial,
    // every tool call returned "MCP server not connected", and the
    // dispatch stalled until the silence timer fired.
    const req: CliResumeRequest = {
      sessionId: "550e8400-e29b-41d4-a716-446655440000",
      prompt: "finish what you started",
      tools: [{ kind: "mcp-http", name: "rimsky-callback", url: "http://x/mcp" }],
      env: {},
    };
    const args = buildClaudeCliResumeArgs(req, { mcpConfigPath: "/tmp/mcp.json" });
    const mcpIdx = args.indexOf("--mcp-config");
    expect(mcpIdx).toBeGreaterThan(-1);
    expect(args[mcpIdx + 1]).toBe("/tmp/mcp.json");
  });

  it("places --resume, --print, and -p in the expected slots", () => {
    const args = buildClaudeCliResumeArgs(
      {
        sessionId: "abc",
        prompt: "U",
        tools: [],
        env: {},
      },
      { mcpConfigPath: "/tmp/mcp.json" },
    );
    expect(args[0]).toBe("--resume");
    expect(args[1]).toBe("abc");
    expect(args[2]).toBe("--print");
    expect(args[args.length - 2]).toBe("-p");
    expect(args[args.length - 1]).toBe("U");
  });

  it("does NOT emit --system-prompt-file on resume (session-restored)", () => {
    const args = buildClaudeCliResumeArgs(
      { sessionId: "abc", prompt: "U", tools: [], env: {} },
      { mcpConfigPath: "/tmp/mcp.json" },
    );
    expect(args).not.toContain("--system-prompt-file");
  });

  it("preserves arg ordering (-p prompt is always last) when all knobs set", () => {
    process.env.RIMSKY_DISPATCH_MAX_USD = "5.00"; // overridden by req
    const args = buildClaudeCliArgs(
      baseReq({
        bare: true,
        permissionMode: "acceptEdits",
        allowedTools: ["Read"],
        disallowedTools: ["Bash"],
        addDirs: ["../specs"],
        maxBudgetUsd: "1.00",
      }),
      PATHS,
    );
    expect(args[args.length - 2]).toBe("-p");
    expect(args[args.length - 1]).toBe("U");
    // --print remains the first arg regardless of knobs.
    expect(args[0]).toBe("--print");
  });
});
