import { describe, it, expect } from "vitest";
import { applyDedupResults } from "./resolve.js";

describe("applyDedupResults", () => {
  it("returns status-update intents for unambiguous duplicates", () => {
    const out = applyDedupResults([
      { duplicateGroups: [{ survivorId: "a", duplicateIds: ["b", "c"] }] },
    ]);
    expect(out).toEqual([
      { findingId: "b", duplicateOf: "a" },
      { findingId: "c", duplicateOf: "a" },
    ]);
  });

  it("conservative: skip when a duplicate is also a survivor elsewhere", () => {
    const out = applyDedupResults([
      { duplicateGroups: [{ survivorId: "a", duplicateIds: ["b"] }] },
      { duplicateGroups: [{ survivorId: "b", duplicateIds: ["c"] }] },
    ]);
    // b is both a duplicate and a survivor — keep it. c → b survives.
    const ids = out.map((o) => o.findingId);
    expect(ids).not.toContain("b");
    expect(ids).toContain("c");
  });

  it("first-mention wins on conflicting duplicate→survivor maps", () => {
    const out = applyDedupResults([
      { duplicateGroups: [{ survivorId: "a", duplicateIds: ["d"] }] },
      { duplicateGroups: [{ survivorId: "z", duplicateIds: ["d"] }] },
    ]);
    expect(out).toEqual([{ findingId: "d", duplicateOf: "a" }]);
  });
});
