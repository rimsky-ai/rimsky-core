import { spawn, ChildProcess } from "node:child_process";
import { writeFile, unlink, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

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
}

export interface CliHandle {
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

export function createClaudeCliRunner(opts?: {
  binaryPath?: string;
}): CliRunner {
  const binary = opts?.binaryPath ?? "claude";
  return {
    async spawn(req: CliSpawnRequest): Promise<CliHandle> {
      const tmp = await mkdtemp(join(tmpdir(), "rimsky-cli-"));
      const systemPromptPath = join(tmp, "system.md");
      const mcpConfigPath = join(tmp, "mcp.json");
      await writeFile(systemPromptPath, req.systemPrompt);

      const mcpServers: Record<string, unknown> = {};
      for (const t of req.tools) {
        if (t.kind === "mcp-http") {
          mcpServers[t.name] = { url: t.url, headers: t.headers ?? {} };
        }
      }
      await writeFile(mcpConfigPath, JSON.stringify({ mcpServers }));

      const child: ChildProcess = spawn(
        binary,
        [
          "--model",
          req.model,
          "--system-prompt-file",
          systemPromptPath,
          "--mcp-config",
          mcpConfigPath,
        ],
        {
          cwd: req.cwd,
          env: { ...process.env, ...req.env },
          stdio: ["pipe", "pipe", "pipe"],
        },
      );
      child.stdin?.end(req.userPrompt);

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
