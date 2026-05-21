import { describe, it, expect } from "vitest";
import { makeStateDeps } from "./test-helpers.js";
import { handleDeferFinding } from "./defer-finding.js";

const NOW = "2026-05-19T12:00:00.000+00:00";

describe("handleDeferFinding", () => {
  it("defers an open finding", async () => {
    const { deps } = await makeStateDeps();
    await deps.store.appendFinding({
      kind: "finding",
      id: "f_1",
      ts: NOW,
      pass_id: "p",
      zone_id: "z",
      session_id: "s",
      class: 1,
      effective_class: 1,
      auto_rerouted: false,
      file: "f.ts",
      line_start: null,
      line_end: null,
      description: "x",
      fingerprint: "sha256:x",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    const tok = deps.tokens.issue({ passId: "p", claimHandleId: "s", issuedAt: 0 });
    const r = await handleDeferFinding(
      { session_token: tok, finding_id: "f_1", reason: "later" },
      deps,
    );
    expect(r.finding_status).toBe("deferred");
  });

  it("returns finding_not_found for unknown id", async () => {
    const { deps } = await makeStateDeps();
    const tok = deps.tokens.issue({ passId: "p", claimHandleId: "s", issuedAt: 0 });
    await expect(
      handleDeferFinding({ session_token: tok, finding_id: "f_nope", reason: "x" }, deps),
    ).rejects.toThrow(/unknown finding/);
  });
});
