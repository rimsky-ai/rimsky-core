// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { spawn, ChildProcess } from "node:child_process";
import { writeFile, unlink, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { buildCliEnv, type CliAuthConfig } from "./cli-env.js";
import { CALLBACK_MCP_SERVER_NAME } from "./internal-mcp-server.js";
import { TOOL_DEFINITIONS } from "./internal-mcp-tools.js";

export const REQUIRED_CALLBACK_TOOLS: string[] = TOOL_DEFINITIONS.map(
  (t) => `mcp__${CALLBACK_MCP_SERVER_NAME}__${t.name}`,
);

export function buildAllowedTools(templateAllowed?: string[]): string[] {
  const merged = [...REQUIRED_CALLBACK_TOOLS, ...(templateAllowed ?? [])];
  return [...new Set(merged)];
}

export type CliToolConfig =
  | {
      kind: "mcp-http";
      name: string;
      url: string;
      headers?: Record<string, string>;
    }
  | {
      kind: "mcp-stdio";
      name: string;
      command: string;
      args?: string[];
      env?: Record<string, string>;
    };

export interface CliSpawnRequest {
  model: string;
  systemPrompt: string;
  userPrompt: string;
  tools: CliToolConfig[];
  env: Record<string, string>;
  cwd?: string;
  sessionId?: string;
  bare?: boolean;
  permissionMode?: string;
  allowedTools?: string[];
  disallowedTools?: string[];
  addDirs?: string[];
  maxBudgetUsd?: string;
}

export interface CliResumeRequest {
  sessionId: string;
  prompt: string;
  tools: CliToolConfig[];
  allowedTools?: string[];
  env: Record<string, string>;
  cwd?: string;
}

export interface CliHandle {
  readonly pid?: number;
  onStdout(cb: (chunk: string) => void): void;
  onStderr(cb: (chunk: string) => void): void;
  onExit(
    cb: (exitCode: number | null, signal: NodeJS.Signals | null) => void,
  ): void;
  sendSigterm(): void;
  sendSigkill(): void;
  waitExit(): Promise<{
    exitCode: number | null;
    signal: NodeJS.Signals | null;
  }>;
}

export interface CliRunner {
  spawn(req: CliSpawnRequest): Promise<CliHandle>;
  resume?(req: CliResumeRequest): Promise<CliHandle>;
}

export function buildClaudeCliArgs(
  req: CliSpawnRequest,
  paths: { systemPromptPath: string; mcpConfigPath: string },
): string[] {
  const permissionMode = req.permissionMode ?? "bypassPermissions";
  const maxBudgetUsd = req.maxBudgetUsd ?? process.env.RIMSKY_DISPATCH_MAX_USD;
  return [
    "--print",
    "--output-format",
    "stream-json",
    "--verbose",
    "--model",
    req.model,
    "--permission-mode",
    permissionMode,
    ...(req.sessionId ? ["--session-id", req.sessionId] : []),
    ...(req.bare ? ["--bare"] : []),
    "--allowedTools",
    buildAllowedTools(req.allowedTools).join(" "),
    ...(req.disallowedTools && req.disallowedTools.length > 0
      ? ["--disallowedTools", req.disallowedTools.join(" ")]
      : []),
    ...(req.addDirs && req.addDirs.length > 0
      ? ["--add-dir", ...req.addDirs]
      : []),
    "--system-prompt-file",
    paths.systemPromptPath,
    "--mcp-config",
    paths.mcpConfigPath,
    ...(maxBudgetUsd ? ["--max-budget-usd", maxBudgetUsd] : []),
    "-p",
    req.userPrompt,
  ];
}

export function buildClaudeCliResumeArgs(
  req: CliResumeRequest,
  paths: { mcpConfigPath: string },
): string[] {
  return [
    "--resume",
    req.sessionId,
    "--print",
    "--output-format",
    "stream-json",
    "--verbose",
    "--allowedTools",
    buildAllowedTools(req.allowedTools).join(" "),
    "--mcp-config",
    paths.mcpConfigPath,
    "-p",
    req.prompt,
  ];
}

function mcpConfigJson(tools: CliToolConfig[]): string {
  const mcpServers: Record<string, unknown> = {};
  for (const t of tools) {
    if (t.kind === "mcp-http") {
      mcpServers[t.name] = {
        type: "http",
        url: t.url,
        headers: t.headers ?? {},
      };
    } else if (t.kind === "mcp-stdio") {
      mcpServers[t.name] = {
        type: "stdio",
        command: t.command,
        ...(t.args && t.args.length > 0 ? { args: t.args } : {}),
        ...(t.env ? { env: t.env } : {}),
      };
    }
  }
  return JSON.stringify({ mcpServers });
}

function buildHandleFromChild(
  child: ChildProcess,
  onCleanup: () => void,
): CliHandle {
  const stdoutCbs: ((chunk: string) => void)[] = [];
  const stderrCbs: ((chunk: string) => void)[] = [];
  const exitCbs: ((
    code: number | null,
    signal: NodeJS.Signals | null,
  ) => void)[] = [];
  type ExitResult = {
    exitCode: number | null;
    signal: NodeJS.Signals | null;
  };
  let exited = false;
  let exitResult: ExitResult | null = null;
  const exitWaiters: ((r: ExitResult) => void)[] = [];

  child.stdout?.setEncoding("utf-8");
  child.stderr?.setEncoding("utf-8");
  child.stdout?.on("data", (chunk: string) => {
    for (const cb of stdoutCbs) cb(chunk);
  });
  child.stderr?.on("data", (chunk: string) => {
    for (const cb of stderrCbs) cb(chunk);
  });

  let cleanupRan = false;
  const runCleanup = (): void => {
    if (cleanupRan) return;
    cleanupRan = true;
    try { onCleanup(); } catch {}
  };

  child.on("exit", (code, signal) => {
    exited = true;
    const result: ExitResult = { exitCode: code, signal };
    exitResult = result;
    for (const cb of exitCbs) cb(code, signal);
    for (const w of exitWaiters) w(result);
    runCleanup();
  });

  child.on("error", (err) => {
    for (const cb of stderrCbs) cb(`[spawn error] ${String(err)}\n`);
    if (!exited) {
      exited = true;
      const result: ExitResult = { exitCode: null, signal: null };
      exitResult = result;
      for (const cb of exitCbs) cb(null, null);
      for (const w of exitWaiters) w(result);
    }
    runCleanup();
  });

  return {
    pid: child.pid,
    onStdout: (cb) => { stdoutCbs.push(cb); },
    onStderr: (cb) => { stderrCbs.push(cb); },
    onExit: (cb) => { exitCbs.push(cb); },
    sendSigterm: () => { child.kill("SIGTERM"); },
    sendSigkill: () => { child.kill("SIGKILL"); },
    waitExit: () =>
      exited && exitResult
        ? Promise.resolve(exitResult)
        : new Promise((resolve) => exitWaiters.push(resolve)),
  };
}

export function createClaudeCliRunner(opts: {
  auth: CliAuthConfig;
  binaryPath?: string;
  exposeEnvNames?: readonly string[];
}): CliRunner {
  const binary = opts.binaryPath ?? "claude";
  const auth = opts.auth;
  const exposeEnvNames = opts.exposeEnvNames ?? [];
  const collectExposedEnv = (): Record<string, string> => {
    const out: Record<string, string> = {};
    for (const name of exposeEnvNames) {
      const v = process.env[name];
      if (v !== undefined) out[name] = v;
    }
    return out;
  };
  return {
    async spawn(req: CliSpawnRequest): Promise<CliHandle> {
      const tmp = await mkdtemp(join(tmpdir(), "rimsky-cli-"));
      const systemPromptPath = join(tmp, "system.md");
      const mcpConfigPath = join(tmp, "mcp.json");
      await writeFile(systemPromptPath, req.systemPrompt);

      await writeFile(mcpConfigPath, mcpConfigJson(req.tools));

      const { env: authEnv, cleanup: cleanupAuthEnv } = buildCliEnv(auth);
      const args = buildClaudeCliArgs(req, { systemPromptPath, mcpConfigPath });
      const child: ChildProcess = spawn(binary, args, {
        cwd: req.cwd,
        env: { ...collectExposedEnv(), ...authEnv, ...req.env },
        stdio: ["ignore", "pipe", "pipe"],
      });

      return buildHandleFromChild(child, () => {
        void unlink(systemPromptPath).catch(() => {});
        void unlink(mcpConfigPath).catch(() => {});
        void rm(tmp, { recursive: true, force: true }).catch(() => {});
        try { cleanupAuthEnv(); } catch {}
      });
    },
    async resume(req: CliResumeRequest): Promise<CliHandle> {
      const tmp = await mkdtemp(join(tmpdir(), "rimsky-cli-"));
      const mcpConfigPath = join(tmp, "mcp.json");
      await writeFile(mcpConfigPath, mcpConfigJson(req.tools));

      const { env: authEnv, cleanup: cleanupAuthEnv } = buildCliEnv(auth);
      const args = buildClaudeCliResumeArgs(req, { mcpConfigPath });
      const child: ChildProcess = spawn(binary, args, {
        cwd: req.cwd,
        env: { ...collectExposedEnv(), ...authEnv, ...req.env },
        stdio: ["ignore", "pipe", "pipe"],
      });
      return buildHandleFromChild(child, () => {
        void unlink(mcpConfigPath).catch(() => {});
        void rm(tmp, { recursive: true, force: true }).catch(() => {});
        try { cleanupAuthEnv(); } catch {}
      });
    },
  };
}
