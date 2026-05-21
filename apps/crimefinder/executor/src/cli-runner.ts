/**
 * @source: executors/claude-agent/src/cli-runner.ts
 * @diverged: true
 * @reason: removed cwd_from_store and attribute-writeback paths; crimefinder
 *          uses a fixed cwd (the repo root) and routes state via
 *          review-finding / review-commit-fix gates instead of attribute
 *          writebacks. CLI tuning surface is narrowed to model + max-turns.
 *          System prompt now passed via `--system-prompt-file` (matched to
 *          claude-agent), not inline `--system-prompt` — avoids argv limits
 *          as templates grow and keeps the longer prompt out of `ps`.
 */

import { spawn } from "node:child_process";
import type { ChildProcess } from "node:child_process";
import { writeFile, chmod, unlink, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

export interface CliSpawnRequest {
  bin: string;
  mcpConfigPath: string;
  allowedTools: string[];
  cwd: string;
  systemPrompt: string;
  userPrompt: string;
  env: Record<string, string>;
  model?: string;
  sessionId?: string;
  maxTurns?: number;
}

export interface CliRunResult {
  exitCode: number | null;
  stdoutTail: string;
  stderrTail: string;
}

const TAIL = 200;

function tail(buf: string): string {
  const lines = buf.split("\n");
  if (lines.length <= TAIL) return buf;
  return lines.slice(lines.length - TAIL).join("\n");
}

export interface CliHandle {
  spawn(req: CliSpawnRequest, onStdoutLine: (line: string) => void): Promise<CliRunResult>;
}

/**
 * Builds the argv passed to the claude binary.
 *
 * Exported for tests so the args composition can be asserted directly
 * without spawning a real subprocess. The system prompt is delivered via
 * a chmod-0600 tmpfile (`--system-prompt-file`) for parity with the
 * upstream claude-agent runner — keeps the long prompt out of `ps` and
 * sidesteps OS-level argv length limits as templates grow.
 */
export function buildClaudeCliArgs(
  req: CliSpawnRequest,
  paths: { systemPromptPath: string },
): string[] {
  const args: string[] = [
    "--mcp-config",
    req.mcpConfigPath,
    "--allowedTools",
    req.allowedTools.join(","),
    "--system-prompt-file",
    paths.systemPromptPath,
    "-p",
    req.userPrompt,
  ];
  if (req.model) args.push("--model", req.model);
  if (req.sessionId) args.push("--session-id", req.sessionId);
  if (req.maxTurns) args.push("--max-turns", String(req.maxTurns));
  return args;
}

export function createClaudeCliRunner(): CliHandle {
  return {
    async spawn(req, onStdoutLine): Promise<CliRunResult> {
      const tmpDir = await mkdtemp(join(tmpdir(), "crimefinder-cli-"));
      const systemPromptPath = join(tmpDir, "system.md");
      await writeFile(systemPromptPath, req.systemPrompt);
      // chmod 0600 — the prompt may carry mission specifics; restrict to
      // the executor process owner (matches upstream claude-agent runner).
      await chmod(systemPromptPath, 0o600);

      const args = buildClaudeCliArgs(req, { systemPromptPath });

      const cleanup = async (): Promise<void> => {
        try { await unlink(systemPromptPath); } catch { /* ignore */ }
        try { await rm(tmpDir, { recursive: true, force: true }); } catch { /* ignore */ }
      };

      let child: ChildProcess;
      try {
        child = spawn(req.bin, args, {
          cwd: req.cwd,
          env: req.env,
          stdio: ["ignore", "pipe", "pipe"],
        });
      } catch (err) {
        await cleanup();
        throw err;
      }

      let stdoutBuf = "";
      let stderrBuf = "";
      child.stdout?.setEncoding("utf-8");
      child.stderr?.setEncoding("utf-8");
      child.stdout?.on("data", (chunk: string) => {
        stdoutBuf += chunk;
        let nl: number;
        while ((nl = stdoutBuf.indexOf("\n")) !== -1) {
          const line = stdoutBuf.slice(0, nl);
          stdoutBuf = stdoutBuf.slice(nl + 1);
          onStdoutLine(line);
        }
      });
      child.stderr?.on("data", (chunk: string) => {
        stderrBuf += chunk;
      });

      return new Promise<CliRunResult>((resolve) => {
        let settled = false;
        const finish = (result: CliRunResult): void => {
          if (settled) return;
          settled = true;
          void cleanup().finally(() => resolve(result));
        };
        child.on("close", (code) => {
          finish({ exitCode: code, stdoutTail: tail(stdoutBuf), stderrTail: tail(stderrBuf) });
        });
        child.on("error", () => {
          finish({ exitCode: null, stdoutTail: tail(stdoutBuf), stderrTail: tail(stderrBuf) });
        });
      });
    },
  };
}
