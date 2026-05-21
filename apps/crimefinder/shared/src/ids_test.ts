import { describe, it, expect } from "vitest";
import {
  generatePassId,
  generateFindingId,
  generateZoneId,
  generateRowId,
  generateSessionToken,
} from "./ids.js";

describe("ids", () => {
  it("generatePassId returns 26-char string starting with p_", () => {
    const id = generatePassId();
    expect(id).toMatch(/^p_[a-z2-7]{24}$/);
    expect(id.length).toBe(26);
  });

  it("generateFindingId returns 26-char string starting with f_", () => {
    const id = generateFindingId();
    expect(id).toMatch(/^f_[a-z2-7]{24}$/);
    expect(id.length).toBe(26);
  });

  it("generateZoneId is deterministic for the same label", () => {
    expect(generateZoneId("src/feature_a")).toBe(generateZoneId("src/feature_a"));
  });

  it("different zone labels produce different IDs", () => {
    expect(generateZoneId("a")).not.toBe(generateZoneId("b"));
  });

  it("zone ID starts with z_ and is 14 chars total", () => {
    const id = generateZoneId("src/foo");
    expect(id).toMatch(/^z_[a-z2-7]{12}$/);
    expect(id.length).toBe(14);
  });

  it("generateRowId returns unique IDs across 1000 calls", () => {
    const set = new Set<string>();
    for (let i = 0; i < 1000; i++) set.add(generateRowId());
    expect(set.size).toBe(1000);
  });

  it("generateSessionToken returns 32-char base32 strings", () => {
    const t = generateSessionToken();
    expect(t).toMatch(/^[a-z2-7]{32}$/);
  });
});
