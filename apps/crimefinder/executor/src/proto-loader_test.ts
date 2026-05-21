import { describe, it, expect } from "vitest";
import { loadExecutorProtos } from "./proto-loader.js";

describe("loadExecutorProtos", () => {
  it("loads Executor + ExecutorObservability + CrimefinderState", () => {
    const pkg = loadExecutorProtos();
    expect(pkg.rimsky.v1.Executor.service).toBeDefined();
    expect(pkg.rimsky.v1.ExecutorObservability.service).toBeDefined();
    expect(pkg.crimefinder.v1.CrimefinderState.service).toBeDefined();
    expect(Object.keys(pkg.rimsky.v1.Executor.service).length).toBeGreaterThan(0);
    expect(Object.keys(pkg.crimefinder.v1.CrimefinderState.service).length).toBeGreaterThan(0);
  });
});
