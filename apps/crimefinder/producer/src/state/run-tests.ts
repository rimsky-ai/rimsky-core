import crypto from "node:crypto";
import childProcess from "node:child_process";
import type { Logger } from "pino";
import { JsonlMutex } from "../jsonl-mutex.js";
import type { GitOps } from "../git-ops.js";
import type { TestResult } from "./test-cache.js";
import { TestCache } from "./test-cache.js";

export type ExecFileFn = typeof childProcess.execFile;

export class TestRunMutex extends JsonlMutex {}

export interface RunTestsArgs {
  passId: string;
  repoRoot: string;
  command: string;
  timeoutMs: number;
}

export interface RunTestsDeps {
  git: GitOps;
  cache: TestCache;
  mutex: TestRunMutex;
  execFile?: ExecFileFn;
  logger: Logger;
}

const TAIL_LINES = 200;

function tail(s: string, n: number): string {
  const lines = s.split("\n");
  if (lines.length <= n) return s;
  return lines.slice(lines.length - n).join("\n");
}

function computeCommandSha(command: string, repoRoot: string): string {
  return crypto.createHash("sha256").update(command + " " + repoRoot).digest("hex");
}

// Shell-like tokenizer that respects single/double quotes and backslash
// escapes. `npm test -- --grep "long arg"` parses as four tokens with
// the quoted "long arg" preserved as a single argv entry. Avoids a new
// dependency (string-argv) while still matching its behavior for the
// cases cfg:tests.command is expected to take.
export function tokenizeShellCommand(command: string): string[] {
  const out: string[] = [];
  let i = 0;
  while (i < command.length) {
    while (i < command.length && /\s/.test(command[i])) i++;
    if (i >= command.length) break;
    let cur = "";
    let quote: '"' | "'" | null = null;
    while (i < command.length) {
      const ch = command[i];
      if (quote === null && /\s/.test(ch)) break;
      if (quote === null && (ch === '"' || ch === "'")) {
        quote = ch as '"' | "'";
        i++;
        continue;
      }
      if (quote !== null && ch === quote) {
        quote = null;
        i++;
        continue;
      }
      if (quote !== "'" && ch === "\\" && i + 1 < command.length) {
        cur += command[i + 1];
        i += 2;
        continue;
      }
      cur += ch;
      i++;
    }
    out.push(cur);
  }
  return out;
}

export async function runTests(args: RunTestsArgs, deps: RunTestsDeps): Promise<TestResult> {
  const commandSha = computeCommandSha(args.command, args.repoRoot);
  const currentMtime = await deps.git.mtime(args.repoRoot);
  return deps.mutex.withLock(async () => {
    const hit = deps.cache.get(args.passId, currentMtime, commandSha);
    if (hit) {
      deps.logger.debug({ passId: args.passId }, "test_cache_hit");
      return hit;
    }
    const parts = tokenizeShellCommand(args.command);
    if (parts.length === 0) {
      throw new Error("empty test command");
    }
    const [program, ...argv] = parts;
    const exec = deps.execFile ?? childProcess.execFile;
    const result = await new Promise<TestResult>((resolve) => {
      const ranAt = new Date().toISOString();
      const child = exec(
        program,
        argv,
        { cwd: args.repoRoot, timeout: args.timeoutMs, maxBuffer: 16 * 1024 * 1024 },
        (err, stdout, stderr) => {
          const e = err as (NodeJS.ErrnoException & { killed?: boolean; signal?: string }) | null;
          const timedOut =
            e !== null &&
            (e.code === "ETIMEDOUT" ||
              e.killed === true ||
              e.signal === "SIGTERM" ||
              e.signal === "SIGKILL");
          if (timedOut) {
            resolve({
              exitCode: -1,
              stdoutTail: tail(String(stdout ?? ""), TAIL_LINES),
              stderrTail: "TIMEOUT\n" + tail(String(stderr ?? ""), TAIL_LINES),
              ranAt,
              treeMtimeAtRun: currentMtime,
              commandSha,
            });
            return;
          }
          const code = err && typeof (err as { code?: number }).code === "number"
            ? (err as { code: number }).code
            : err
              ? 1
              : 0;
          resolve({
            exitCode: code,
            stdoutTail: tail(String(stdout ?? ""), TAIL_LINES),
            stderrTail: tail(String(stderr ?? ""), TAIL_LINES),
            ranAt,
            treeMtimeAtRun: currentMtime,
            commandSha,
          });
        },
      );
      // Defensive: detach to keep node clean on timeout.
      child.on?.("error", () => undefined);
    });
    deps.cache.set(args.passId, result);
    return result;
  });
}
