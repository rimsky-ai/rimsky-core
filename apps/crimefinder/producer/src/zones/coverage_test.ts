import { describe, it, expect } from "vitest";
import { computeZoneCoverage, mapFileToZone } from "./coverage.js";
import type { Zone } from "./partition.js";

const zones: Zone[] = [
  { id: "z_a", label: "src/a", files: ["src/a/x.ts", "src/a/y.ts"] },
  { id: "z_b", label: "src/b", files: ["src/b/z.ts"] },
];

const NOW = "2026-05-19T12:00:00.000+00:00";

describe("computeZoneCoverage", () => {
  it("computes 100% when all files are covered", () => {
    const rows = [
      { ts: NOW, pass_id: "p_1", session_id: "s1", zone_id: "z_a", file: "src/a/x.ts" },
      { ts: NOW, pass_id: "p_1", session_id: "s1", zone_id: "z_a", file: "src/a/y.ts" },
    ];
    const summary = computeZoneCoverage(rows, "p_1", zones);
    const a = summary.find((s) => s.zoneId === "z_a")!;
    expect(a.filesChecked).toBe(2);
    expect(a.coveragePercent).toBe(100);
  });

  it("computes 50% when half the files are covered", () => {
    const rows = [
      { ts: NOW, pass_id: "p_1", session_id: "s1", zone_id: "z_a", file: "src/a/x.ts" },
    ];
    const summary = computeZoneCoverage(rows, "p_1", zones);
    const a = summary.find((s) => s.zoneId === "z_a")!;
    expect(a.coveragePercent).toBe(50);
  });

  it("ignores rows from other passes", () => {
    const rows = [
      { ts: NOW, pass_id: "p_other", session_id: "s1", zone_id: "z_a", file: "src/a/x.ts" },
    ];
    const summary = computeZoneCoverage(rows, "p_1", zones);
    expect(summary.find((s) => s.zoneId === "z_a")!.filesChecked).toBe(0);
  });
});

describe("mapFileToZone", () => {
  it("returns the zone containing the file", () => {
    expect(mapFileToZone("src/a/x.ts", zones)?.id).toBe("z_a");
    expect(mapFileToZone("src/b/z.ts", zones)?.id).toBe("z_b");
  });
  it("returns null when no zone owns the file", () => {
    expect(mapFileToZone("src/c/q.ts", zones)).toBeNull();
  });
});
