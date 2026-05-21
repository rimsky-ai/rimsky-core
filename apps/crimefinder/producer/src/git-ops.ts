import childProcess from "node:child_process";
import { promisify } from "node:util";
import fs from "node:fs/promises";
import path from "node:path";

export interface GitStatus {
  paths: string[];
  clean: boolean;
}

export interface GitCommit {
  sha: string;
  subject: string;
  body: string;
}

export interface GitOps {
  status(repoRoot: string): Promise<GitStatus>;
  mtime(repoRoot: string): Promise<number>;
  add(repoRoot: string, paths: string[]): Promise<void>;
  commit(repoRoot: string, message: string): Promise<string>;
  log(repoRoot: string, sinceSha?: string): Promise<GitCommit[]>;
  repoRoot(cwd: string): Promise<string>;
  showCommitTimestamp(repoRoot: string, sha: string): Promise<string>;
}

export class GitCommitError extends Error {
  readonly stderr: string;
  constructor(stderr: string) {
    super(`git commit failed: ${stderr}`);
    this.stderr = stderr;
    this.name = "GitCommitError";
  }
}

export type ExecFileFn = typeof childProcess.execFile;

// Promise-wrapper for an injected execFile so tests can substitute a
// mock without monkey-patching child_process.
function makeRun(exec: ExecFileFn): (cwd: string, args: string[]) => Promise<{ stdout: string; stderr: string }> {
  const wrapped = promisify(exec);
  return (cwd, args) =>
    wrapped("git", args, { cwd, maxBuffer: 8 * 1024 * 1024 }) as Promise<{
      stdout: string;
      stderr: string;
    }>;
}

// createGitOps(exec) — pass a mock execFile in tests; production callers
// can use the default. Previously the parameter was named `_exec` and
// ignored, which made the whole gits-ops surface unmockable.
export function createGitOps(exec: ExecFileFn = childProcess.execFile): GitOps {
  const runGit = makeRun(exec);
  const runRaw = promisify(exec);
  return {
    async status(repoRoot: string): Promise<GitStatus> {
      const { stdout } = await runGit(repoRoot, ["status", "--porcelain"]);
      const paths: string[] = [];
      for (const line of stdout.split("\n")) {
        if (!line.trim()) continue;
        // Format: XY <path>; rename has " -> ".
        const rest = line.slice(3);
        const arrowIdx = rest.indexOf(" -> ");
        const p = arrowIdx >= 0 ? rest.slice(arrowIdx + 4) : rest;
        paths.push(p);
      }
      return { paths, clean: paths.length === 0 };
    },

    async mtime(repoRoot: string): Promise<number> {
      // Fast path: the most recent commit's timestamp is the mtime of
      // every tracked file that hasn't changed since. We then `git
      // status --porcelain` for working-tree dirty paths and stat just
      // those, instead of N syscalls across the whole tree.
      let headTs = 0;
      try {
        const { stdout } = await runGit(repoRoot, ["log", "-1", "--format=%ct", "HEAD"]);
        const t = Number(stdout.trim());
        if (Number.isFinite(t)) headTs = t * 1000;
      } catch {
        // empty repo: fall through to status-only scan
      }
      let maxMtime = headTs;
      const { stdout: status } = await runGit(repoRoot, ["status", "--porcelain"]);
      for (const line of status.split("\n")) {
        if (!line.trim()) continue;
        const rest = line.slice(3);
        const arrowIdx = rest.indexOf(" -> ");
        const rel = arrowIdx >= 0 ? rest.slice(arrowIdx + 4) : rest;
        try {
          const st = await fs.stat(path.join(repoRoot, rel));
          if (st.mtimeMs > maxMtime) maxMtime = st.mtimeMs;
        } catch {
          // ignore missing
        }
      }
      return maxMtime;
    },

    async add(repoRoot: string, paths: string[]): Promise<void> {
      if (paths.length === 0) return;
      await runGit(repoRoot, ["add", "--", ...paths]);
    },

    async commit(repoRoot: string, message: string): Promise<string> {
      try {
        await runGit(repoRoot, ["commit", "-m", message]);
      } catch (e) {
        const err = e as { stderr?: string; stdout?: string; message?: string };
        const stderr = (err.stderr ?? "") + (err.stdout ?? "");
        throw new GitCommitError(stderr || err.message || "unknown git failure");
      }
      const { stdout } = await runGit(repoRoot, ["rev-parse", "HEAD"]);
      return stdout.trim();
    },

    async log(repoRoot: string, sinceSha?: string): Promise<GitCommit[]> {
      // Use a unit separator we won't see in commit messages.
      const fmt = "%H%x1f%s%x1f%b%x1e";
      const range = sinceSha ? `${sinceSha}..HEAD` : "-500";
      const args = sinceSha
        ? ["log", "--pretty=format:" + fmt, range]
        : ["log", "--pretty=format:" + fmt, range];
      let stdout: string;
      try {
        const r = await runGit(repoRoot, args);
        stdout = r.stdout;
      } catch (e) {
        // empty repo or bad range — return nothing.
        const err = e as { stderr?: string };
        const stderr = err.stderr ?? "";
        if (stderr.includes("does not have any commits")) return [];
        // Recovery scenario: a rebase/force-push made `sinceSha` unreachable
        // from HEAD. Fall back to a bounded recent-history scan rather than
        // silently returning [] (which would lose every commit since).
        const unreachable =
          sinceSha &&
          (stderr.includes("unknown revision") ||
            stderr.includes("ambiguous argument") ||
            stderr.includes("bad revision"));
        if (unreachable) {
          try {
            const r2 = await runGit(repoRoot, [
              "log",
              "--pretty=format:" + fmt,
              "-500",
            ]);
            stdout = r2.stdout;
          } catch (e2) {
            const err2 = e2 as { stderr?: string };
            if ((err2.stderr ?? "").includes("does not have any commits")) {
              return [];
            }
            throw e2;
          }
        } else {
          throw e;
        }
      }
      const out: GitCommit[] = [];
      for (const rec of stdout.split("\x1e")) {
        if (!rec.trim()) continue;
        const [sha, subject, body] = rec.split("\x1f");
        if (!sha) continue;
        out.push({ sha: sha.trim(), subject: subject ?? "", body: body ?? "" });
      }
      return out;
    },

    async repoRoot(cwd: string): Promise<string> {
      const { stdout } = (await runRaw("git", ["rev-parse", "--show-toplevel"], {
        cwd,
      })) as { stdout: string };
      return stdout.trim();
    },

    async showCommitTimestamp(repoRoot: string, sha: string): Promise<string> {
      const { stdout } = await runGit(repoRoot, ["show", "-s", "--format=%cI", sha]);
      return stdout.trim();
    },
  };
}
