import { describe, it, expect } from "vitest";
import { groupFindingsByFile, batchFileGroups } from "./group.js";
import type { FindingRow } from "@crimefinder/shared";

const NOW = "2026-05-19T12:00:00.000+00:00";

function f(id: string, file: string): FindingRow {
  return {
    kind: "finding",
    id,
    ts: NOW,
    pass_id: "p_1",
    zone_id: "z_1",
    session_id: "s",
    class: 1,
    effective_class: 1,
    auto_rerouted: false,
    file,
    line_start: null,
    line_end: null,
    description: `d-${id}`,
    fingerprint: `sha256:${id}`,
    concept_slug: null,
    tension_slug: null,
    confidence: "high",
    status: "open",
    originating_zone_id: null,
  };
}

describe("groupFindingsByFile", () => {
  it("drops files with only one finding", () => {
    const m = groupFindingsByFile([f("a", "x.ts"), f("b", "y.ts")]);
    expect(m.size).toBe(0);
  });
  it("keeps files with 2+ findings", () => {
    const m = groupFindingsByFile([f("a", "x.ts"), f("b", "x.ts"), f("c", "y.ts")]);
    expect(m.size).toBe(1);
    expect(m.get("x.ts")?.length).toBe(2);
  });
});

describe("batchFileGroups", () => {
  it("packs file groups without exceeding the cap", () => {
    const findings: FindingRow[] = [];
    const groups = new Map<string, string[]>();
    for (let f = 0; f < 5; f++) {
      const ids: string[] = [];
      for (let i = 0; i < 12; i++) ids.push(`id-${f}-${i}`);
      groups.set(`file-${f}.ts`, ids);
    }
    const batches = batchFileGroups(groups, findings, 20);
    for (const b of batches) {
      expect(b.findingCount).toBeLessThanOrEqual(20);
    }
    // 60 unique findings across 20-cap batches → at least 3 batches.
    expect(batches.length).toBeGreaterThanOrEqual(3);
  });
});
