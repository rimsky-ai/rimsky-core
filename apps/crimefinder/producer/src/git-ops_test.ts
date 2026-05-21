import { describe, it, expect, beforeEach } from "vitest";
import fs from "node:fs/promises";
import path from "node:path";
import os from "node:os";
import { promisify } from "node:util";
import { execFile as execFileCb } from "node:child_process";
import { createGitOps, GitCommitError } from "./git-ops.js";

const execFile = promisify(execFileCb);

async function initRepo(dir: string): Promise<void> {
  await execFile("git", ["init", "-q", "-b", "main"], { cwd: dir });
  await execFile("git", ["config", "user.email", "test@example.com"], { cwd: dir });
  await execFile("git", ["config", "user.name", "test"], { cwd: dir });
  await execFile("git", ["config", "commit.gpgsign", "false"], { cwd: dir });
}

describe("git-ops", () => {
  let dir: string;
  const git = createGitOps();

  beforeEach(async () => {
    dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-git-"));
    await initRepo(dir);
  });

  it("status on a clean repo returns no paths", async () => {
    // need at least one commit so the working tree is "clean"
    await fs.writeFile(path.join(dir, "a.txt"), "hello");
    await git.add(dir, ["a.txt"]);
    await git.commit(dir, "init");
    const s = await git.status(dir);
    expect(s.clean).toBe(true);
    expect(s.paths).toEqual([]);
  });

  it("status surfaces uncommitted paths", async () => {
    await fs.writeFile(path.join(dir, "a.txt"), "hello");
    const s = await git.status(dir);
    expect(s.clean).toBe(false);
    expect(s.paths).toContain("a.txt");
  });

  it("commit returns a SHA and shows up in log", async () => {
    await fs.writeFile(path.join(dir, "a.txt"), "x");
    await git.add(dir, ["a.txt"]);
    const sha = await git.commit(dir, "first commit\n\nResolves: f_abc");
    expect(sha).toMatch(/^[0-9a-f]{40}$/);
    const log = await git.log(dir);
    expect(log[0].sha).toBe(sha);
    expect(log[0].body).toContain("Resolves: f_abc");
  });

  it("commit throws GitCommitError when nothing staged", async () => {
    await expect(git.commit(dir, "empty")).rejects.toBeInstanceOf(GitCommitError);
  });

  it("commit throws GitCommitError when pre-commit hook fails", async () => {
    const hookDir = path.join(dir, ".git", "hooks");
    await fs.mkdir(hookDir, { recursive: true });
    const hook = path.join(hookDir, "pre-commit");
    await fs.writeFile(hook, "#!/bin/sh\necho hook says no >&2\nexit 1\n");
    await fs.chmod(hook, 0o755);
    await fs.writeFile(path.join(dir, "a.txt"), "x");
    await git.add(dir, ["a.txt"]);
    await expect(git.commit(dir, "init")).rejects.toBeInstanceOf(GitCommitError);
  });

  it("mtime returns a number once files exist", async () => {
    await fs.writeFile(path.join(dir, "a.txt"), "x");
    await git.add(dir, ["a.txt"]);
    await git.commit(dir, "c");
    const mt = await git.mtime(dir);
    expect(mt).toBeGreaterThan(0);
  });

  it("repoRoot resolves to the top-level dir", async () => {
    expect(await git.repoRoot(dir)).toBe(await fs.realpath(dir));
  });
});
