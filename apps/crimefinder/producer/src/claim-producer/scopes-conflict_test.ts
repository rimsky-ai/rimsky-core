import { describe, it, expect } from "vitest";
import { scopesConflict } from "./scopes-conflict.js";

function bytes(obj: unknown): Uint8Array {
  return new TextEncoder().encode(JSON.stringify(obj));
}

describe("scopesConflict", () => {
  it("disjoint zones don't conflict", () => {
    const a = bytes({ kind: "source-tree-zone", pass_id: "p_1", zone_files: ["a.ts"] });
    const b = bytes({ kind: "source-tree-zone", pass_id: "p_1", zone_files: ["b.ts"] });
    expect(scopesConflict(a, b)).toBe(false);
  });

  it("overlapping zones conflict", () => {
    const a = bytes({ kind: "source-tree-zone", pass_id: "p_1", zone_files: ["a.ts", "b.ts"] });
    const b = bytes({ kind: "source-tree-zone", pass_id: "p_1", zone_files: ["b.ts"] });
    expect(scopesConflict(a, b)).toBe(true);
  });

  it("zones in different passes don't conflict (separate logical workspaces)", () => {
    const a = bytes({ kind: "source-tree-zone", pass_id: "p_1", zone_files: ["a.ts"] });
    const b = bytes({ kind: "source-tree-zone", pass_id: "p_2", zone_files: ["a.ts"] });
    expect(scopesConflict(a, b)).toBe(false);
  });

  it("same-pass pass-state conflicts (single-holder)", () => {
    const a = bytes({ kind: "pass-state", pass_id: "p_1" });
    const b = bytes({ kind: "pass-state", pass_id: "p_1" });
    expect(scopesConflict(a, b)).toBe(true);
  });

  it("different-pass pass-state does not conflict", () => {
    expect(
      scopesConflict(
        bytes({ kind: "pass-state", pass_id: "p_1" }),
        bytes({ kind: "pass-state", pass_id: "p_2" }),
      ),
    ).toBe(false);
  });

  it("byte-equal fallback for malformed bytes", () => {
    const a = new TextEncoder().encode("not-json-A");
    const b = new TextEncoder().encode("not-json-A");
    expect(scopesConflict(a, b)).toBe(true);
    const c = new TextEncoder().encode("not-json-B");
    expect(scopesConflict(a, c)).toBe(false);
  });
});
