// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  buildClaudeCliArgs,
  buildClaudeCliResumeArgs,
  REQUIRED_CALLBACK_TOOLS,
  type CliResumeRequest,
  type CliSpawnRequest,
} from "./cli-runner.js";
import { CALLBACK_MCP_SERVER_NAME } from "./internal-mcp-server.js";
import { TOOL_DEFINITIONS } from "./internal-mcp-tools.js";

const PATHS = {
  systemPromptPath: "/tmp/sys.md",
  mcpConfigPath: "/tmp/mcp.json",
} as const;

// The always-allowlisted callback tool surface, joined as the CLI receives
// it. Derived the same way the source does so the assertion can't drift.
const CALLBACK_TOOLS_JOINED = REQUIRED_CALLBACK_TOOLS.join(" ");

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
      // Always present, even with no per-template allowed_tools — the
      // rimsky-callback surface must clear Claude Code's deferred-MCP
      // permission gate.
      "--allowedTools",
      CALLBACK_TOOLS_JOINED,
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

  it("merges per-template allowedTools onto the callback surface (union, de-duped) and joins disallowedTools with spaces", () => {
    const args = buildClaudeCliArgs(
      baseReq({
        // "mcp__rimsky-callback__report_complete" is already in the always-on
        // callback surface, so the union must de-dupe it (appear once).
        allowedTools: ["Read", "Edit", "mcp__rimsky-callback__report_complete"],
        disallowedTools: ["Bash"],
      }),
      PATHS,
    );
    const aIdx = args.indexOf("--allowedTools");
    expect(aIdx).toBeGreaterThan(-1);
    const allowed = (args[aIdx + 1] as string).split(" ");
    // Callback tools come first, then the template extras.
    expect(allowed.slice(0, REQUIRED_CALLBACK_TOOLS.length)).toEqual(
      REQUIRED_CALLBACK_TOOLS,
    );
    expect(allowed).toContain("Read");
    expect(allowed).toContain("Edit");
    // De-duped: report_complete appears exactly once even though the template
    // also listed it.
    expect(
      allowed.filter((t) => t === "mcp__rimsky-callback__report_complete"),
    ).toHaveLength(1);
    const dIdx = args.indexOf("--disallowedTools");
    expect(dIdx).toBeGreaterThan(-1);
    expect(args[dIdx + 1]).toBe("Bash");
  });

  it("always emits --allowedTools with the callback surface even when the template specifies none", () => {
    // Empty arrays (no per-template allowed_tools) must NOT drop the
    // rimsky-callback allowlist — bug 3 was bypassPermissions no longer
    // covering the deferred-MCP tool surface in Claude Code 2.1.x.
    const args = buildClaudeCliArgs(
      baseReq({ allowedTools: [], disallowedTools: [] }),
      PATHS,
    );
    const aIdx = args.indexOf("--allowedTools");
    expect(aIdx).toBeGreaterThan(-1);
    expect(args[aIdx + 1]).toBe(CALLBACK_TOOLS_JOINED);
    // disallowedTools genuinely empty → flag absent.
    expect(args).not.toContain("--disallowedTools");
  });

  it("derives the callback tool names from TOOL_DEFINITIONS + the server-name constant (no hardcoded list)", () => {
    const expected = TOOL_DEFINITIONS.map(
      (t) => `mcp__${CALLBACK_MCP_SERVER_NAME}__${t.name}`,
    );
    expect(REQUIRED_CALLBACK_TOOLS).toEqual(expected);
    // Sanity: the terminal-outcome tool the bug report names is present.
    expect(REQUIRED_CALLBACK_TOOLS).toContain(
      "mcp__rimsky-callback__report_complete",
    );
    // emit_named_event is auto-included because the allowlist derives from
    // TOOL_DEFINITIONS — adding the tool needs no manual allowlist edit.
    expect(REQUIRED_CALLBACK_TOOLS).toContain(
      "mcp__rimsky-callback__emit_named_event",
    );
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

  it("always emits --allowedTools with the callback surface on resume (bug 3: deferred-MCP gate blocks the recovery report_complete)", () => {
    // --allowedTools is process-local invocation config, NOT restored from
    // session state, so the resume argv must re-emit the callback surface or
    // the very report_complete we resumed to make gets gated.
    const args = buildClaudeCliResumeArgs(
      { sessionId: "abc", prompt: "U", tools: [], env: {} },
      { mcpConfigPath: "/tmp/mcp.json" },
    );
    const aIdx = args.indexOf("--allowedTools");
    expect(aIdx).toBeGreaterThan(-1);
    expect(args[aIdx + 1]).toBe(CALLBACK_TOOLS_JOINED);
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
