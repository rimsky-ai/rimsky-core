import { describe, it, expect } from "vitest";
import fs from "node:fs/promises";
import path from "node:path";
import { GateError } from "@crimefinder/shared";
import { makeStateDeps } from "./test-helpers.js";
import { handleAppendCoverage } from "./append-coverage.js";

describe("handleAppendCoverage", () => {
  it("appends one coverage row per file that exists under the repo root", async () => {
    const { dir, deps } = await makeStateDeps();
    await fs.writeFile(path.join(dir, "a.ts"), "x");
    await fs.writeFile(path.join(dir, "b.ts"), "y");
    const tok = deps.tokens.issue({
      passId: "p_1",
      claimHandleId: "s",
      zoneId: "z_1",
      role: "review-zone",
      issuedAt: 0,
    });
    const r = await handleAppendCoverage(
      { session_token: tok, files_read: ["a.ts", "b.ts"] },
      deps,
    );
    expect(r.recorded_count).toBe(2);
    const rows = await deps.store.readCoverage();
    expect(rows).toHaveLength(2);
  });

  it("rejects with coverage_file_missing when a cited file is not present", async () => {
    const { deps } = await makeStateDeps();
    const tok = deps.tokens.issue({
      passId: "p_1",
      claimHandleId: "s",
      zoneId: "z_1",
      role: "review-zone",
      issuedAt: 0,
    });
    let caught: unknown;
    try {
      await handleAppendCoverage(
        { session_token: tok, files_read: ["does-not-exist.ts"] },
        deps,
      );
    } catch (e) {
      caught = e;
    }
    expect(caught).toBeInstanceOf(GateError);
    expect((caught as GateError).envelope.data.crimefinder_error_class).toBe(
      "coverage_file_missing",
    );
  });

  it("rejects with wrong_session_role when called from a fix-cycle session", async () => {
    const { dir, deps } = await makeStateDeps();
    await fs.writeFile(path.join(dir, "a.ts"), "x");
    const tok = deps.tokens.issue({
      passId: "p_1",
      claimHandleId: "s",
      zoneId: "z_1",
      role: "fix-cycle",
      issuedAt: 0,
    });
    let caught: unknown;
    try {
      await handleAppendCoverage({ session_token: tok, files_read: ["a.ts"] }, deps);
    } catch (e) {
      caught = e;
    }
    expect(caught).toBeInstanceOf(GateError);
    expect((caught as GateError).envelope.data.crimefinder_error_class).toBe(
      "wrong_session_role",
    );
  });

  it("rejects path-traversal attempts as coverage_file_escaped", async () => {
    // Path-traversal attempts get a dedicated error class so they aren't
    // lumped in with benign "you typed the wrong filename" errors. The
    // security signal matters: an agent that tried to exfiltrate via "../"
    // should be visible in logs and metrics.
    const { deps } = await makeStateDeps();
    const tok = deps.tokens.issue({
      passId: "p_1",
      claimHandleId: "s",
      zoneId: "z_1",
      role: "review-zone",
      issuedAt: 0,
    });
    let caught: unknown;
    try {
      await handleAppendCoverage(
        { session_token: tok, files_read: ["../../etc/passwd"] },
        deps,
      );
    } catch (e) {
      caught = e;
    }
    expect(caught).toBeInstanceOf(GateError);
    expect((caught as GateError).envelope.data.crimefinder_error_class).toBe(
      "coverage_file_escaped",
    );
  });
});
