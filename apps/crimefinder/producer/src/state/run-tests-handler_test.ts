import { describe, it, expect } from "vitest";
import fs from "node:fs/promises";
import path from "node:path";
import { promisify } from "node:util";
import { execFile as execFileCb } from "node:child_process";
import { makeStateDeps } from "./test-helpers.js";
import { handleRunTests } from "./run-tests-handler.js";

const execFile = promisify(execFileCb);

async function initRepo(dir: string): Promise<void> {
  await execFile("git", ["init", "-q", "-b", "main"], { cwd: dir });
  await execFile("git", ["config", "user.email", "x@y"], { cwd: dir });
  await execFile("git", ["config", "user.name", "t"], { cwd: dir });
  await execFile("git", ["config", "commit.gpgsign", "false"], { cwd: dir });
  await fs.writeFile(path.join(dir, "a"), "x");
  await execFile("git", ["add", "."], { cwd: dir });
  await execFile("git", ["commit", "-qm", "i"], { cwd: dir });
}

describe("handleRunTests", () => {
  it("runs configured command and caches subsequent identical calls", async () => {
    const { dir, deps } = await makeStateDeps({
      config: { tests: { command: "true", timeout_seconds: 5, cwd: "." } },
    });
    await initRepo(dir);
    const tok = deps.tokens.issue({ passId: "p", claimHandleId: "s", issuedAt: 0 });
    const a = await handleRunTests({ session_token: tok }, deps);
    const b = await handleRunTests({ session_token: tok }, deps);
    expect(a.exit_code).toBe(0);
    expect(b.ran_at).toBe(a.ran_at);
    expect(b.cached).toBe(true);
  });

  it("returns test_command_not_configured when tests is unset", async () => {
    const { dir, deps } = await makeStateDeps();
    await initRepo(dir);
    const tok = deps.tokens.issue({ passId: "p", claimHandleId: "s", issuedAt: 0 });
    await expect(handleRunTests({ session_token: tok }, deps)).rejects.toThrow(
      /config.tests is missing/,
    );
  });

  it("rejects when called from a dedup session", async () => {
    const { dir, deps } = await makeStateDeps({
      config: { tests: { command: "true", timeout_seconds: 5, cwd: "." } },
    });
    await initRepo(dir);
    const tok = deps.tokens.issue({
      passId: "p",
      claimHandleId: "s",
      role: "dedup",
      issuedAt: 0,
    });
    await expect(handleRunTests({ session_token: tok }, deps)).rejects.toThrow(
      /review_run_tests is not available to dedup sessions/,
    );
  });
});
