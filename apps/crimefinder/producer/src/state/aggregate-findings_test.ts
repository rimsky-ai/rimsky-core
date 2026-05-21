import { describe, it, expect } from "vitest";
import { makeStateDeps } from "./test-helpers.js";
import { handleAggregateFindings } from "./aggregate-findings.js";

const NOW = "2026-05-19T12:00:00.000+00:00";

describe("handleAggregateFindings", () => {
  it("returns class_1_4_remaining + class_5 + dedup groups", async () => {
    const { deps } = await makeStateDeps();
    for (const id of ["a", "b"]) {
      await deps.store.appendFinding({
        kind: "finding",
        id,
        ts: NOW,
        pass_id: "p_1",
        zone_id: "z_1",
        session_id: "s",
        class: 1,
        effective_class: 1,
        auto_rerouted: false,
        file: "src/x.ts",
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
    const tok = deps.tokens.issue({ passId: "p_1", claimHandleId: "s", issuedAt: 0 });
    const r = await handleAggregateFindings({ session_token: tok, pass_id: "p_1" }, deps);
    expect(r.class_1_4_remaining).toBe(2);
    const groups = JSON.parse(new TextDecoder().decode(r.dedup_file_groups_json));
    expect(groups).toEqual([{ file: "src/x.ts", finding_ids: ["a", "b"] }]);
  });
});
