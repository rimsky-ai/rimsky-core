import { describe, it, expect } from "vitest";
import { encodeClass, decodeClass, isFindingClass } from "./class-codec.js";

describe("class-codec", () => {
  it.each([1, 2, 3, 4] as const)("round-trips numeric class %s", (c) => {
    expect(decodeClass(encodeClass(c))).toBe(c);
  });

  it.each(["5a", "5b"] as const)("round-trips string class %s", (c) => {
    expect(decodeClass(encodeClass(c))).toBe(c);
  });

  it.each(["0", "5", "5c", "", "x"])("rejects invalid wire %s", (s) => {
    expect(() => decodeClass(s)).toThrow();
  });

  it("rejects non-string inputs", () => {
    expect(() => decodeClass(1 as unknown as string)).toThrow();
  });

  it("isFindingClass narrows correctly", () => {
    expect(isFindingClass(1)).toBe(true);
    expect(isFindingClass("5b")).toBe(true);
    expect(isFindingClass(7)).toBe(false);
    expect(isFindingClass("5c")).toBe(false);
  });
});
