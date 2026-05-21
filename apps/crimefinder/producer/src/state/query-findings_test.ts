import { describe, it, expect } from "vitest";
import { makeStateDeps } from "./test-helpers.js";
import { handleQueryFindings } from "./query-findings.js";

const NOW = "2026-05-19T12:00:00.000+00:00";

describe("handleQueryFindings", () => {
  it("returns findings filtered by pass and zone", async () => {
    const { deps } = await makeStateDeps();
    await deps.store.appendFinding({
      kind: "finding",
      id: "a",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_a",
      session_id: "s",
      class: 1,
      effective_class: 1,
      auto_rerouted: false,
      file: "x.ts",
      line_start: null,
      line_end: null,
      description: "x",
      fingerprint: "sha256:a",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    const tok = deps.tokens.issue({ passId: "p_1", claimHandleId: "s", issuedAt: 0 });
    const r = await handleQueryFindings({ session_token: tok, zone_id: "z_a" }, deps);
    const arr = JSON.parse(new TextDecoder().decode(r.findings_json));
    expect(arr).toHaveLength(1);
    expect(arr[0].id).toBe("a");
  });

  it("UNAUTHENTICATED on bad token", async () => {
    const { deps } = await makeStateDeps();
    await expect(handleQueryFindings({ session_token: "x" }, deps)).rejects.toThrow();
  });
});
