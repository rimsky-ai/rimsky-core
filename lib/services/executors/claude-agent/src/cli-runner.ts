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

/**
 * Fully-qualified names of the executor's own internal MCP callback tools,
 * as the Claude CLI namespaces them: `mcp__${server}__<toolName>`.
 *
 * Derived programmatically from {@link TOOL_DEFINITIONS} (which already folds
 * in `ATTRIBUTES_TOOL_DEFINITIONS`) and the single
 * {@link CALLBACK_MCP_SERVER_NAME} constant, so adding or renaming a callback
 * tool keeps the allowlist in sync with zero manual edits here.
 *
 * Why this must always be allowlisted: the executor spawns the CLI with
 * `--permission-mode bypassPermissions`, but Claude Code 2.1.x moved MCP
 * tools onto a deferred permission surface that bypassPermissions no longer
 * covers. Without these names in `--allowedTools`, the agent's
 * `report_complete` / `report_park` / etc. calls are blocked with
 * "Claude requested permissions to use mcp__rimsky-callback__…" and the
 * dispatch never lands a terminal outcome (notably on 2nd+ dispatches in a
 * container and on resume).
 */
export const REQUIRED_CALLBACK_TOOLS: string[] = TOOL_DEFINITIONS.map(
  (t) => `mcp__${CALLBACK_MCP_SERVER_NAME}__${t.name}`,
);

/**
 * Unions the always-required callback tools with any per-template allowed
 * tools, de-duplicating while preserving order (callback tools first, then
 * the template's extras). The result is the value passed to
 * `--allowedTools`, which is always emitted so the callback surface is never
 * gated by Claude Code's permission prompt.
 */
export function buildAllowedTools(templateAllowed?: string[]): string[] {
  const merged = [...REQUIRED_CALLBACK_TOOLS, ...(templateAllowed ?? [])];
  return [...new Set(merged)];
}

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
 *
 * A single MCP server wired into the spawned CLI's `--mcp-config`.
 *
 * Two leaf shapes, discriminated by `kind`:
 *   - `mcp-http`  : a remote (or loopback) streamable-HTTP MCP server the
 *                   CLI dials by URL. The internal rimsky-callback server,
 *                   host `cli.mcp_servers` http servers, and the
 *                   per-dispatch loopback listeners that front `module` /
 *                   `http-loopback` catalog servers all resolve to this leaf.
 *   - `mcp-stdio` : a local MCP server the CLI spawns as a subprocess
 *                   (`command` + `args`, optional `env`). Catalog `stdio`
 *                   servers resolve to this leaf; `mcpConfigJson` emits a
 *                   `type: "stdio"` entry rather than `type: "http"`.
 */
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
  /**
   * UUID-formatted session identifier passed to the CLI as `--session-id`.
   * Required for the post-exit resume-with-prompt-injection retry path —
   * if the subprocess exits with code 0 but never calls report_complete,
   * agent-run.ts uses this id with `--resume` to recover the session and
   * inject a reminder prompt. Reusing the rimsky `runId` (already a UUID)
   * gives stable trace correlation across the original spawn, the retry,
   * and downstream rimsky events.
   */
  sessionId?: string;
  /**
   * Per-template CLI tuning, sourced from `attributes.cli.*` (spec §5.8 +
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

/**
 * Args for {@link CliRunner.resume}. Resumes a prior session by id and
 * delivers a single follow-up user prompt.
 *
 * The CLI session restores its model and system prompt from the prior
 * session's persisted state. MCP config, however, is process-local
 * runtime configuration — it is NOT part of session state and must be
 * re-passed on every invocation. Without `--mcp-config` the resumed
 * subprocess has no `rimsky-callback` server to dial, so any tool call
 * the agent makes returns "MCP server not connected" and the dispatch
 * cannot finish.
 *
 * Used by agent-run.ts's exit-watcher when the subprocess exits with
 * code 0 without ever calling `mcp__rimsky-callback__report_complete`,
 * and by the J10 resume-after-park path.
 */
export interface CliResumeRequest {
  sessionId: string;
  prompt: string;
  tools: CliToolConfig[];
  /**
   * Per-template + host-server allow list, mirroring
   * {@link CliSpawnRequest.allowedTools}. `--allowedTools` is process-local
   * invocation config (NOT restored from session state by `--resume`), so a
   * resumed dispatch that wants to reach host validator MCP servers must
   * re-emit their tool names here — otherwise Claude Code's deferred-MCP
   * permission gate blocks the very calls the resume exists to make. The
   * required `rimsky-callback` tools are always folded in by
   * {@link buildAllowedTools} regardless of this value.
   */
  allowedTools?: string[];
  env: Record<string, string>;
  cwd?: string;
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
  /**
   * Resume a previously-spawned session by id and deliver a follow-up
   * user prompt. Used by agent-run.ts's clean-exit-without-report
   * recovery path. The resumed CLI replays the session's conversation
   * (prior model, system prompt, MCP config, tool history) and sees
   * only `prompt` as the new user message.
   *
   * Optional on the interface: fake runners used in tests can omit it
   * (callers detect absence and skip retry).
   */
  resume?(req: CliResumeRequest): Promise<CliHandle>;
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
 *
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
 * Optional knobs forwarded from `attributes.cli.*` are spliced in
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
    // @deliberate: stream-json + --verbose make the CLI emit incremental NDJSON events
    // (one per assistant message, tool call, tool result, etc.) on stdout
    // while it works — without this, an orchestrator pattern that waits on
    // long Task subagent calls produces no parent stdout for minutes,
    // tripping the executor's silence-timer. Matches brain's
    // judging-orchestrator.ts spawn args.
    "--output-format",
    "stream-json",
    "--verbose",
    "--model",
    req.model,
    "--permission-mode",
    permissionMode,
    ...(req.sessionId ? ["--session-id", req.sessionId] : []),
    ...(req.bare ? ["--bare"] : []),
    // @deliberate: always emit --allowedTools: the executor's own rimsky-callback MCP
    // tools must be allowlisted regardless of the per-template config, or
    // Claude Code 2.1.x's deferred-MCP permission surface blocks the
    // terminal callback even under bypassPermissions. Per-template tools
    // are merged on top (union, de-duplicated).
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

/**
 * Builds the argv passed to the `claude` binary for a resume invocation.
 *
 * `--resume <sessionId>` restores the prior conversation, model, and
 * system prompt from session state. It does NOT restore `--mcp-config`,
 * which is process-local runtime configuration — so the resume argv
 * MUST include `--mcp-config` pointing at a freshly-written file or the
 * resumed subprocess has no MCP servers registered and every tool call
 * fails with "MCP server not connected."
 *
 * Exported for tests so the resume argv composition can be asserted
 * directly without spawning a real subprocess.
 */
export function buildClaudeCliResumeArgs(
  req: CliResumeRequest,
  paths: { mcpConfigPath: string },
): string[] {
  return [
    "--resume",
    req.sessionId,
    "--print",
    // @deliberate: keep stream-json + verbose for parity with spawn — the executor's
    // silence-tracker still watches stdout, and the resume run is
    // typically short (one prompt → one tool call).
    "--output-format",
    "stream-json",
    "--verbose",
    // @deliberate: resume runs the reminder/park-recovery prompt whose whole purpose is to
    // land a rimsky-callback terminal outcome, so the callback tools MUST be
    // allowlisted here too — otherwise the deferred-MCP permission gate
    // blocks the very call we resumed to make. `--allowedTools` is NOT
    // restored from session state (it is process-local invocation config,
    // like --mcp-config), so it must be re-emitted on every resume. Any
    // host-server allow entries threaded in via `req.allowedTools` are
    // unioned on top so a resumed dispatch can still reach the host
    // validator MCP servers.
    "--allowedTools",
    buildAllowedTools(req.allowedTools).join(" "),
    "--mcp-config",
    paths.mcpConfigPath,
    "-p",
    req.prompt,
  ];
}

/**
 * Serializes `tools` to a `mcp.json` payload matching Claude CLI's
 * `--mcp-config` schema. Shared by spawn() and resume(): both
 * invocations need the same MCP config on disk because `--mcp-config`
 * is process-local runtime config, not session state — `--resume` does
 * NOT carry it forward.
 */
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
      // @deliberate: omit `env` entirely when unset so the spawned stdio MCP
      // subprocess inherits the CLI's environment, matching the Claude CLI's
      // own default for `--mcp-config` stdio leaves.
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

/**
 * Wraps a spawned ChildProcess in the CliHandle observer surface.
 * Shared by spawn() and resume() so both produce identical handles.
 */
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
    try { onCleanup(); } catch { /* @deliberate: ignore */ }
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
}): CliRunner {
  const binary = opts.binaryPath ?? "claude";
  const auth = opts.auth;
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
        env: { ...authEnv, ...req.env },
        stdio: ["ignore", "pipe", "pipe"],
      });

      return buildHandleFromChild(child, () => {
        void unlink(systemPromptPath).catch(() => {});
        void unlink(mcpConfigPath).catch(() => {});
        void rm(tmp, { recursive: true, force: true }).catch(() => {});
        try { cleanupAuthEnv(); } catch { /* @deliberate: ignore */ }
      });
    },
    async resume(req: CliResumeRequest): Promise<CliHandle> {
      // @deliberate: the CLI session carries the model + system prompt across resume,
      // so we don't re-pass `--system-prompt-file`. MCP config, however,
      // is NOT session-persisted — it's process-local runtime config —
      // so we must write a fresh `mcp.json` and pass `--mcp-config` on
      // every resume. Without it the resumed subprocess has no
      // `rimsky-callback` server registered, every tool call returns
      // "MCP server not connected", and the dispatch stalls.
      //
      // The auth env is also rebuilt: the original spawn's was cleaned
      // up at exit.
      const tmp = await mkdtemp(join(tmpdir(), "rimsky-cli-"));
      const mcpConfigPath = join(tmp, "mcp.json");
      await writeFile(mcpConfigPath, mcpConfigJson(req.tools));

      const { env: authEnv, cleanup: cleanupAuthEnv } = buildCliEnv(auth);
      const args = buildClaudeCliResumeArgs(req, { mcpConfigPath });
      const child: ChildProcess = spawn(binary, args, {
        cwd: req.cwd,
        env: { ...authEnv, ...req.env },
        stdio: ["ignore", "pipe", "pipe"],
      });
      return buildHandleFromChild(child, () => {
        void unlink(mcpConfigPath).catch(() => {});
        void rm(tmp, { recursive: true, force: true }).catch(() => {});
        try { cleanupAuthEnv(); } catch { /* @deliberate: ignore */ }
      });
    },
  };
}
