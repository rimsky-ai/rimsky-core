import { describe, it, expect } from "vitest";
import { TestCache } from "./test-cache.js";

describe("TestCache", () => {
  const base = {
    exitCode: 0,
    stdoutTail: "ok",
    stderrTail: "",
    ranAt: "now",
    treeMtimeAtRun: 100,
    commandSha: "sha-A",
  };

  it("hits when mtime did not advance", () => {
    const c = new TestCache();
    c.set("p_1", base);
    expect(c.get("p_1", 100, "sha-A")).toEqual(base);
  });

  it("misses when current mtime advanced", () => {
    const c = new TestCache();
    c.set("p_1", base);
    expect(c.get("p_1", 200, "sha-A")).toBeNull();
  });

  it("misses when commandSha differs", () => {
    const c = new TestCache();
    c.set("p_1", base);
    expect(c.get("p_1", 100, "sha-B")).toBeNull();
  });

  it("isolates passes", () => {
    const c = new TestCache();
    c.set("p_1", base);
    expect(c.get("p_2", 100, "sha-A")).toBeNull();
  });
});
