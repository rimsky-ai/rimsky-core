import { describe, it, expect } from "vitest";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import { execFile as execFileCb } from "node:child_process";
import pino from "pino";
import { createGitOps } from "./git-ops.js";
import { startHealthServer } from "./health.js";

const execFile = promisify(execFileCb);
const logger = pino({ level: "silent" });

async function initRepo(dir: string): Promise<void> {
  await execFile("git", ["init", "-q", "-b", "main"], { cwd: dir });
  await execFile("git", ["config", "user.email", "x@y"], { cwd: dir });
  await execFile("git", ["config", "user.name", "t"], { cwd: dir });
  await execFile("git", ["config", "commit.gpgsign", "false"], { cwd: dir });
  await fs.writeFile(path.join(dir, "x"), "1");
  await execFile("git", ["add", "."], { cwd: dir });
  await execFile("git", ["commit", "-qm", "i"], { cwd: dir });
}

describe("health", () => {
  it("returns 200 when the repo is reachable", async () => {
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-health-"));
    await initRepo(dir);
    const h = await startHealthServer({ repoRoot: dir, port: 0, git: createGitOps(), logger });
    try {
      const r = await h.app.inject({ method: "GET", url: "/health" });
      expect(r.statusCode).toBe(200);
      expect(r.json()).toEqual({ status: "ok" });
    } finally {
      await h.shutdown();
    }
  });

  it("returns 503 when git fails", async () => {
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-health-bad-"));
    // No git init — git status will fail.
    const h = await startHealthServer({ repoRoot: dir, port: 0, git: createGitOps(), logger });
    try {
      const r = await h.app.inject({ method: "GET", url: "/health" });
      expect(r.statusCode).toBe(503);
    } finally {
      await h.shutdown();
    }
  });
});
