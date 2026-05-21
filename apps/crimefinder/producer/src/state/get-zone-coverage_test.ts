import { describe, it, expect } from "vitest";
import { makeStateDeps } from "./test-helpers.js";
import { handleGetZoneCoverage } from "./get-zone-coverage.js";

const NOW = () => new Date().toISOString();

function seedThreeZones(partitionCache: ReturnType<typeof import("../scopes/types.js").createPartitionCache>) {
  const passId = "p_1";
  partitionCache.setZonePlan(passId, [
    { id: "z_1", label: "src/a", files: ["src/a/1.ts", "src/a/2.ts", "src/a/3.ts", "src/a/4.ts", "src/a/5.ts"] },
    { id: "z_2", label: "src/b", files: ["src/b/1.ts", "src/b/2.ts", "src/b/3.ts", "src/b/4.ts", "src/b/5.ts"] },
    { id: "z_3", label: "src/c", files: ["src/c/1.ts", "src/c/2.ts", "src/c/3.ts", "src/c/4.ts", "src/c/5.ts"] },
  ]);
  return passId;
}

describe("handleGetZoneCoverage", () => {
  it("does NOT report pass_complete when other zones have only partial (sub-threshold) coverage", async () => {
    const { deps } = await makeStateDeps({
      config: { coverage: { threshold_pct: 80, on_below_threshold: "require_skip" } },
    });
    const passId = seedThreeZones(deps.partitionCache);

    // Zone 3 (current session) fully covered (5/5 = 100%).
    // Zone 1: 1/5 = 20% coverage (sub-threshold).
    // Zone 2: 1/5 = 20% coverage (sub-threshold).
    // Previously the test "any coverage row counts" would mark all done.
    await deps.store.appendCoverage({ ts: NOW(), pass_id: passId, session_id: "s_1", zone_id: "z_1", file: "src/a/1.ts" });
    await deps.store.appendCoverage({ ts: NOW(), pass_id: passId, session_id: "s_2", zone_id: "z_2", file: "src/b/1.ts" });
    for (const f of ["src/c/1.ts", "src/c/2.ts", "src/c/3.ts", "src/c/4.ts", "src/c/5.ts"]) {
      await deps.store.appendCoverage({ ts: NOW(), pass_id: passId, session_id: "s_3", zone_id: "z_3", file: f });
    }
    const tok = deps.tokens.issue({
      passId,
      claimHandleId: "s_3",
      zoneId: "z_3",
      role: "review-zone",
      issuedAt: 0,
    });
    const r = await handleGetZoneCoverage({ session_token: tok }, deps);
    expect(r.coverage_pct).toBe(100);
    expect(r.pass_complete).toBe(false);
  });

  it("reports pass_complete when every zone meets threshold OR has a skip_zone row", async () => {
    const { deps } = await makeStateDeps({
      config: { coverage: { threshold_pct: 80, on_below_threshold: "require_skip" } },
    });
    const passId = seedThreeZones(deps.partitionCache);
    // Zone 1: 4/5 = 80% (at threshold).
    for (const f of ["src/a/1.ts", "src/a/2.ts", "src/a/3.ts", "src/a/4.ts"]) {
      await deps.store.appendCoverage({ ts: NOW(), pass_id: passId, session_id: "s_1", zone_id: "z_1", file: f });
    }
    // Zone 2: skipped (any coverage, but skip row).
    await deps.store.appendSkipZone({
      kind: "skip_zone",
      id: "sk_1",
      ts: NOW(),
      pass_id: passId,
      zone_id: "z_2",
      session_id: "s_2",
      reason: "irrelevant",
    });
    // Zone 3 (current): 5/5 = 100%.
    for (const f of ["src/c/1.ts", "src/c/2.ts", "src/c/3.ts", "src/c/4.ts", "src/c/5.ts"]) {
      await deps.store.appendCoverage({ ts: NOW(), pass_id: passId, session_id: "s_3", zone_id: "z_3", file: f });
    }
    const tok = deps.tokens.issue({
      passId,
      claimHandleId: "s_3",
      zoneId: "z_3",
      role: "review-zone",
      issuedAt: 0,
    });
    const r = await handleGetZoneCoverage({ session_token: tok }, deps);
    expect(r.pass_complete).toBe(true);
    expect(r.pass_summary_json.length).toBeGreaterThan(0);
    const summary = JSON.parse(new TextDecoder().decode(r.pass_summary_json));
    expect(summary.zones_planned).toBe(3);
    expect(summary.zones_skipped).toBe(1);
    expect(summary.zones_completed).toBe(2);
  });

  it("emits pass_complete:true exactly once under concurrent zone completions (de-dup)", async () => {
    const { deps } = await makeStateDeps({
      config: { coverage: { threshold_pct: 80, on_below_threshold: "require_skip" } },
    });
    const passId = seedThreeZones(deps.partitionCache);
    // All three zones meet threshold.
    for (const z of ["z_1", "z_2", "z_3"]) {
      for (let i = 1; i <= 5; i++) {
        await deps.store.appendCoverage({
          ts: NOW(),
          pass_id: passId,
          session_id: `s_${z}`,
          zone_id: z,
          file: `src/${z === "z_1" ? "a" : z === "z_2" ? "b" : "c"}/${i}.ts`,
        });
      }
    }
    const tokens = ["z_1", "z_2", "z_3"].map((z) =>
      deps.tokens.issue({
        passId,
        claimHandleId: `s_${z}`,
        zoneId: z,
        role: "review-zone",
        issuedAt: 0,
      }),
    );
    // Fire N concurrent gets — each computes allDone:true, but only the
    // first writer of the `pass_closed_emitted` row sees pass_complete:true.
    const results = await Promise.all(
      tokens.map((t) => handleGetZoneCoverage({ session_token: t }, deps)),
    );
    const trueCount = results.filter((r) => r.pass_complete === true).length;
    expect(trueCount).toBe(1);
  });
});
