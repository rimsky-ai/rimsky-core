// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { spawn, ChildProcess } from "node:child_process";
import { writeFile, unlink, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { buildCliEnv, type CliAuthConfig } from "./cli-env.js";

/**
 * CLI runner abstraction — launches an agentic CLI subprocess (e.g. the
 * Claude CLI) and exposes a small control surface for the executor.
 *
 * @source rimsky/src/supervisor/cli-runner.ts
 *
 * Two implementations:
 *   - {@link createClaudeCliRunner} — production: spawns the real `claude`
 *     binary with system prompt + MCP config passed via tmpfiles and user
 *     prompt via stdin.
 *   - Fake runners for tests construct their own {@link CliHandle}.
 */
export interface CliToolConfig {
  kind: "mcp-http";
  name: string;
  url: string;
  headers?: Record<string, string>;
}

export interface CliSpawnRequest {
  model: string;
  systemPrompt: string;
  userPrompt: string;
  tools: CliToolConfig[];
  env: Record<string, string>;
  cwd?: string;
  /**
   * Per-template CLI tuning, sourced from `userdata.cli.*` (spec §5.8 +
   * docs/concepts/executor-claude-agent.md). All optional; defaults
   * preserve current behavior. The executor maps each field to one or
   * more spawn args without rimsky inspecting them.
   *
   * Field mapping (claude --help):
   *   bare              → `--bare`
   *   permissionMode    → `--permission-mode <mode>`
   *   allowedTools      → `--allowedTools <space-separated list>`
   *   disallowedTools   → `--disallowedTools <space-separated list>`
   *   addDirs           → `--add-dir <path1> <path2> …`
   *   maxBudgetUsd      → `--max-budget-usd <amount>`
   *
   * `maxBudgetUsd` retains an env-var fallback (`RIMSKY_DISPATCH_MAX_USD`)
   * for deployment-wide caps; the per-template value here wins when set.
   */
  bare?: boolean;
  permissionMode?: string;
  allowedTools?: string[];
  disallowedTools?: string[];
  addDirs?: string[];
  maxBudgetUsd?: string;
}

export interface CliHandle {
  /** Subprocess PID once the child is alive. Undefined for fake handles
   * that don't spawn a real process. Useful for trace-correlation logs. */
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
}

/**
 * Production CLI runner. Spawns the real `claude` binary in a hermetic
 * environment built by {@link buildCliEnv}: API-key mode uses an
 * apiKeyHelper so the key never enters the child env; OAuth mode passes
 * `CLAUDE_CODE_OAUTH_TOKEN` through. The parent `process.env` is NOT
 * inherited — only the auth env, plus the per-run env passed in
 * `CliSpawnRequest.env` (e.g. `RIMSKY_CALLBACK_URL`,
 * `RIMSKY_CALLBACK_TOKEN`), reaches the subprocess. This keeps unrelated
 * pod env (DB DSNs, internal callback secrets) out of the CLI.
 */
/**
 * Builds the argv passed to the `claude` binary for one dispatch.
 *
 * Args follow the working pattern from skillprompting/brain
 * (`judging-orchestrator.ts`). The CLI surface:
 *   - `--print` enables non-interactive print mode (required when stdin/
 *     stdout are pipes; the executor's silence-tracker reads stdout
 *     incrementally so plain text is fine — `stream-json` is an
 *     enhancement for downstream parsing, not required).
 *   - `--permission-mode <mode>` defaults to `bypassPermissions` so
 *     edits and MCP tool calls (the `rimsky-callback` surface)
 *     proceed without an interactive approval prompt. The container's
 *     bind-mount + non-root user is the real sandbox; layering
 *     claude's permission gate on top just blocks the agent from
 *     calling the tools rimsky needs it to call. `acceptEdits` is too
 *     narrow — auto-approves Edit/Write but still gates MCP and
 *     out-of-cwd reads.
 *   - `--system-prompt-file` injects the rendered system prompt.
 *   - `--mcp-config` points at the per-dispatch MCP config that
 *     exposes the `rimsky-callback` tool surface.
 *   - `-p <prompt>` provides the user prompt as the positional arg;
 *     stdin is closed (`stdio[0] = "ignore"`).
 *
 * Optional knobs forwarded from `userdata.cli.*` are spliced in
 * conditionally — each defaults to current behavior, so existing
 * templates are unaffected. `maxBudgetUsd` retains an env-var fallback
 * (`RIMSKY_DISPATCH_MAX_USD`) so deployments can cap spend without
 * touching every template; the per-template value wins when set.
 *
 * Exported for tests so the args composition can be asserted directly
 * without spawning a real subprocess.
 */
export function buildClaudeCliArgs(
  req: CliSpawnRequest,
  paths: { systemPromptPath: string; mcpConfigPath: string },
): string[] {
  const permissionMode = req.permissionMode ?? "bypassPermissions";
  const maxBudgetUsd = req.maxBudgetUsd ?? process.env.RIMSKY_DISPATCH_MAX_USD;
  return [
    "--print",
    "--model",
    req.model,
    "--permission-mode",
    permissionMode,
    ...(req.bare ? ["--bare"] : []),
    ...(req.allowedTools && req.allowedTools.length > 0
      ? ["--allowedTools", req.allowedTools.join(" ")]
      : []),
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

export function createClaudeCliRunner(opts: {
  auth: CliAuthConfig;
  binaryPath?: string;
}): CliRunner {
  const binary = opts.binaryPath ?? "claude";
  const auth = opts.auth;
  return {
    async spawn(req: CliSpawnRequest): Promise<CliHandle> {
      const tmp = await mkdtemp(join(tmpdir(), "rimsky-cli-"));
      const systemPromptPath = join(tmp, "system.md");
      const mcpConfigPath = join(tmp, "mcp.json");
      await writeFile(systemPromptPath, req.systemPrompt);

      const mcpServers: Record<string, unknown> = {};
      for (const t of req.tools) {
        if (t.kind === "mcp-http") {
          mcpServers[t.name] = {
            type: "http",
            url: t.url,
            headers: t.headers ?? {},
          };
        }
      }
      await writeFile(mcpConfigPath, JSON.stringify({ mcpServers }));

      const { env: authEnv, cleanup: cleanupAuthEnv } = buildCliEnv(auth);
      const args = buildClaudeCliArgs(req, { systemPromptPath, mcpConfigPath });
      const child: ChildProcess = spawn(binary, args, {
        cwd: req.cwd,
        env: { ...authEnv, ...req.env },
        stdio: ["ignore", "pipe", "pipe"],
      });

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
        void unlink(systemPromptPath).catch(() => {});
        void unlink(mcpConfigPath).catch(() => {});
        void rm(tmp, { recursive: true, force: true }).catch(() => {});
        try { cleanupAuthEnv(); } catch { /* ignore */ }
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
        onStdout: (cb) => {
          stdoutCbs.push(cb);
        },
        onStderr: (cb) => {
          stderrCbs.push(cb);
        },
        onExit: (cb) => {
          exitCbs.push(cb);
        },
        sendSigterm: () => {
          child.kill("SIGTERM");
        },
        sendSigkill: () => {
          child.kill("SIGKILL");
        },
        waitExit: () =>
          exited && exitResult
            ? Promise.resolve(exitResult)
            : new Promise((resolve) => exitWaiters.push(resolve)),
      };
    },
  };
}
