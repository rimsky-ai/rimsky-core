import { describe, it, expect, beforeEach } from "vitest";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import { execFile as execFileCb } from "node:child_process";
import pino from "pino";
import { TestCache } from "./test-cache.js";
import { runTests, TestRunMutex } from "./run-tests.js";
import { createGitOps } from "../git-ops.js";

const execFile = promisify(execFileCb);
const logger = pino({ level: "silent" });

async function initRepo(dir: string): Promise<void> {
  await execFile("git", ["init", "-q", "-b", "main"], { cwd: dir });
  await execFile("git", ["config", "user.email", "x@y.z"], { cwd: dir });
  await execFile("git", ["config", "user.name", "t"], { cwd: dir });
  await execFile("git", ["config", "commit.gpgsign", "false"], { cwd: dir });
  await fs.writeFile(path.join(dir, "a.txt"), "x");
  await execFile("git", ["add", "."], { cwd: dir });
  await execFile("git", ["commit", "-qm", "init"], { cwd: dir });
}

describe("runTests", () => {
  let dir: string;
  beforeEach(async () => {
    dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-tests-"));
    await initRepo(dir);
  });

  it("runs the command then caches the result", async () => {
    const cache = new TestCache();
    const mutex = new TestRunMutex();
    const git = createGitOps();
    const first = await runTests(
      { passId: "p_1", repoRoot: dir, command: "true", timeoutMs: 5000 },
      { git, cache, mutex, logger },
    );
    expect(first.exitCode).toBe(0);

    const second = await runTests(
      { passId: "p_1", repoRoot: dir, command: "true", timeoutMs: 5000 },
      { git, cache, mutex, logger },
    );
    // Same ranAt iff cached.
    expect(second.ranAt).toBe(first.ranAt);
  });

  it("re-runs when the working tree advances", async () => {
    const cache = new TestCache();
    const mutex = new TestRunMutex();
    const git = createGitOps();
    const first = await runTests(
      { passId: "p_1", repoRoot: dir, command: "true", timeoutMs: 5000 },
      { git, cache, mutex, logger },
    );
    // Touch a tracked file to bump mtime.
    await new Promise((r) => setTimeout(r, 10));
    await fs.writeFile(path.join(dir, "a.txt"), "y");
    const second = await runTests(
      { passId: "p_1", repoRoot: dir, command: "true", timeoutMs: 5000 },
      { git, cache, mutex, logger },
    );
    expect(second.ranAt).not.toBe(first.ranAt);
  });

  it("re-runs when the command changes", async () => {
    const cache = new TestCache();
    const mutex = new TestRunMutex();
    const git = createGitOps();
    const first = await runTests(
      { passId: "p_1", repoRoot: dir, command: "true", timeoutMs: 5000 },
      { git, cache, mutex, logger },
    );
    const second = await runTests(
      { passId: "p_1", repoRoot: dir, command: "false", timeoutMs: 5000 },
      { git, cache, mutex, logger },
    );
    expect(second.exitCode).not.toBe(first.exitCode);
  });

  it("returns exitCode -1 on timeout", async () => {
    const cache = new TestCache();
    const mutex = new TestRunMutex();
    const git = createGitOps();
    const result = await runTests(
      { passId: "p_1", repoRoot: dir, command: "sleep 5", timeoutMs: 200 },
      { git, cache, mutex, logger },
    );
    expect(result.exitCode).toBe(-1);
    expect(result.stderrTail).toContain("TIMEOUT");
  });
});
