import { describe, it, expect } from "vitest";
import fs from "node:fs/promises";
import path from "node:path";
import { promisify } from "node:util";
import { execFile as execFileCb } from "node:child_process";
import { makeStateDeps } from "./test-helpers.js";
import { handleCommitFix } from "./commit-fix.js";
import { GateError } from "@crimefinder/shared";

const execFile = promisify(execFileCb);

const NOW = "2026-05-19T12:00:00.000+00:00";

async function initRepoWithFile(dir: string, filename: string): Promise<void> {
  await execFile("git", ["init", "-q", "-b", "main"], { cwd: dir });
  await execFile("git", ["config", "user.email", "x@y"], { cwd: dir });
  await execFile("git", ["config", "user.name", "t"], { cwd: dir });
  await execFile("git", ["config", "commit.gpgsign", "false"], { cwd: dir });
  // Ignore .crimefinder/ so JSONL writes don't dirty the working tree
  // (in production, the consumer commits .crimefinder/; here we keep the
  // test isolated).
  await fs.writeFile(path.join(dir, ".gitignore"), ".crimefinder/\n");
  await fs.writeFile(path.join(dir, filename), "x");
  await execFile("git", ["add", "."], { cwd: dir });
  await execFile("git", ["commit", "-qm", "init"], { cwd: dir });
}

async function addFinding(deps: Awaited<ReturnType<typeof makeStateDeps>>["deps"], id: string, file: string) {
  await deps.store.appendFinding({
    kind: "finding",
    id,
    ts: NOW,
    pass_id: "p",
    zone_id: "z",
    session_id: "s",
    class: 1,
    effective_class: 1,
    auto_rerouted: false,
    file,
    line_start: null,
    line_end: null,
    description: id,
    fingerprint: `sha256:${id}`,
    concept_slug: null,
    tension_slug: null,
    confidence: "high",
    status: "open",
    originating_zone_id: null,
  });
}

describe("handleCommitFix", () => {
  it("happy path: commits the changed file and appends status:fixed", async () => {
    const { dir, deps } = await makeStateDeps();
    await initRepoWithFile(dir, "src.ts");
    await addFinding(deps, "f_x", "src.ts");
    await fs.writeFile(path.join(dir, "src.ts"), "fixed");
    const tok = deps.tokens.issue({ passId: "p", claimHandleId: "s", issuedAt: 0 });
    const r = await handleCommitFix(
      { session_token: tok, finding_id: "f_x", fix_description: "fix", commit_message: "fix bug" },
      deps,
    );
    expect(r.commit_sha).toMatch(/^[0-9a-f]{40}$/);
    expect(r.finding_status).toBe("fixed");
    const rows = await deps.store.readFindings();
    expect(rows.some((row) => row.kind === "status_update" && row.status === "fixed")).toBe(true);
  });

  it("rejects when finding doesn't exist", async () => {
    const { dir, deps } = await makeStateDeps();
    await initRepoWithFile(dir, "src.ts");
    await fs.writeFile(path.join(dir, "src.ts"), "y");
    const tok = deps.tokens.issue({ passId: "p", claimHandleId: "s", issuedAt: 0 });
    await expect(
      handleCommitFix(
        { session_token: tok, finding_id: "f_nope", fix_description: "x", commit_message: "x" },
        deps,
      ),
    ).rejects.toBeInstanceOf(GateError);
  });

  it("rejects when finding is already fixed", async () => {
    const { dir, deps } = await makeStateDeps();
    await initRepoWithFile(dir, "src.ts");
    await addFinding(deps, "f_x", "src.ts");
    await deps.store.appendFinding({
      kind: "status_update",
      id: "su_1",
      ts: NOW,
      ref: "f_x",
      status: "fixed",
      by_pass: "p",
      by_session: "s",
      resolved_at_commit: "abc",
    });
    await fs.writeFile(path.join(dir, "src.ts"), "y");
    const tok = deps.tokens.issue({ passId: "p", claimHandleId: "s", issuedAt: 0 });
    await expect(
      handleCommitFix(
        { session_token: tok, finding_id: "f_x", fix_description: "x", commit_message: "x" },
        deps,
      ),
    ).rejects.toBeInstanceOf(GateError);
  });

  it("rejects when working tree is clean", async () => {
    const { dir, deps } = await makeStateDeps();
    await initRepoWithFile(dir, "src.ts");
    await addFinding(deps, "f_x", "src.ts");
    const tok = deps.tokens.issue({ passId: "p", claimHandleId: "s", issuedAt: 0 });
    await expect(
      handleCommitFix(
        { session_token: tok, finding_id: "f_x", fix_description: "x", commit_message: "x" },
        deps,
      ),
    ).rejects.toMatchObject({ envelope: { data: { crimefinder_error_class: "working_tree_clean" } } });
  });

  it("rejects when changed paths don't overlap finding scope", async () => {
    const { dir, deps } = await makeStateDeps();
    await initRepoWithFile(dir, "other.ts");
    await addFinding(deps, "f_x", "src.ts");
    await fs.writeFile(path.join(dir, "other.ts"), "y");
    const tok = deps.tokens.issue({ passId: "p", claimHandleId: "s", issuedAt: 0 });
    await expect(
      handleCommitFix(
        { session_token: tok, finding_id: "f_x", fix_description: "x", commit_message: "x" },
        deps,
      ),
    ).rejects.toMatchObject({
      envelope: { data: { crimefinder_error_class: "working_tree_changes_out_of_scope" } },
    });
  });

  it("commit_failed surfaces stderr_excerpt", async () => {
    const { dir, deps } = await makeStateDeps();
    await initRepoWithFile(dir, "src.ts");
    const hookDir = path.join(dir, ".git", "hooks");
    await fs.mkdir(hookDir, { recursive: true });
    const hook = path.join(hookDir, "pre-commit");
    await fs.writeFile(hook, "#!/bin/sh\necho HOOK_REJECTED >&2\nexit 1\n");
    await fs.chmod(hook, 0o755);
    await addFinding(deps, "f_x", "src.ts");
    await fs.writeFile(path.join(dir, "src.ts"), "y");
    const tok = deps.tokens.issue({ passId: "p", claimHandleId: "s", issuedAt: 0 });
    await expect(
      handleCommitFix(
        { session_token: tok, finding_id: "f_x", fix_description: "x", commit_message: "x" },
        deps,
      ),
    ).rejects.toMatchObject({ envelope: { data: { crimefinder_error_class: "commit_failed" } } });
  });
});
