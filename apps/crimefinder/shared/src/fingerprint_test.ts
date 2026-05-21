import { describe, it, expect } from "vitest";
import { computeFingerprint, normalizeDescription } from "./fingerprint.js";

describe("computeFingerprint", () => {
  it("is deterministic for the same inputs", () => {
    const a = computeFingerprint({ file: "src/foo.ts", symbol: "bar", description: "missing return" });
    const b = computeFingerprint({ file: "src/foo.ts", symbol: "bar", description: "missing return" });
    expect(a).toBe(b);
  });

  it("ignores line numbers in description (digit normalization)", () => {
    const a = computeFingerprint({ file: "src/foo.ts", description: "error at line 42" });
    const b = computeFingerprint({ file: "src/foo.ts", description: "error at line 99" });
    expect(a).toBe(b);
  });

  it("differs when file path differs", () => {
    const a = computeFingerprint({ file: "src/foo.ts", description: "x" });
    const b = computeFingerprint({ file: "src/bar.ts", description: "x" });
    expect(a).not.toBe(b);
  });

  it("differs when symbol differs (case-sensitive)", () => {
    const a = computeFingerprint({ file: "f.ts", symbol: "Foo", description: "x" });
    const b = computeFingerprint({ file: "f.ts", symbol: "foo", description: "x" });
    expect(a).not.toBe(b);
  });

  it("collapses markdown emphasis differences", () => {
    const a = computeFingerprint({ file: "f.ts", description: "**bug** here" });
    const b = computeFingerprint({ file: "f.ts", description: "bug here" });
    expect(a).toBe(b);
  });

  it("normalizes hex addresses", () => {
    const a = computeFingerprint({ file: "f.ts", description: "ptr 0xdeadbeef invalid" });
    const b = computeFingerprint({ file: "f.ts", description: "ptr 0xcafe1234 invalid" });
    expect(a).toBe(b);
  });

  it("normalizes UUIDs", () => {
    const a = computeFingerprint({
      file: "f.ts",
      description: "session 12345678-1234-1234-1234-123456789abc rejected",
    });
    const b = computeFingerprint({
      file: "f.ts",
      description: "session abcdef00-aaaa-bbbb-cccc-dddddddddddd rejected",
    });
    expect(a).toBe(b);
  });

  it("returns a sha256-prefixed hex string", () => {
    const fp = computeFingerprint({ file: "f.ts", description: "x" });
    expect(fp).toMatch(/^sha256:[0-9a-f]{64}$/);
  });

  it("normalizeDescription collapses whitespace and trims", () => {
    expect(normalizeDescription("  many\n\tspaces   here  ")).toBe("many spaces here");
  });
});
