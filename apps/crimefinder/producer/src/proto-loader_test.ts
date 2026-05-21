import { describe, it, expect } from "vitest";
import { loadProducerProtos } from "./proto-loader.js";

describe("proto-loader", () => {
  it("loads ClaimProducer and CrimefinderState services", () => {
    const pkg = loadProducerProtos();
    expect(pkg.rimsky.v1.ClaimProducer.service).toBeDefined();
    expect(pkg.crimefinder.v1.CrimefinderState.service).toBeDefined();
    expect(Object.keys(pkg.rimsky.v1.ClaimProducer.service).length).toBeGreaterThan(0);
    expect(Object.keys(pkg.crimefinder.v1.CrimefinderState.service).length).toBeGreaterThan(0);
  });

  it("CrimefinderState defines all expected RPCs", () => {
    const pkg = loadProducerProtos();
    const methods = Object.keys(pkg.crimefinder.v1.CrimefinderState.service);
    for (const m of [
      "AppendFinding",
      "QueryFindings",
      "UpdateFindingStatus",
      "AppendCoverage",
      "RunTests",
      "CommitFix",
      "DeferFinding",
      "SkipZone",
      "RequestHelp",
      "AggregateFindings",
    ]) {
      expect(methods).toContain(m);
    }
  });
});
