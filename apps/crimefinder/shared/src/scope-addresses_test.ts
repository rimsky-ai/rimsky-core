import { describe, it, expect } from "vitest";
import {
  encodeAddress,
  decodeAddress,
  PassStateAddressSchema,
  SourceTreeZoneAddressSchema,
  encodeScopeIdentity,
} from "./scope-addresses.js";

describe("scope-addresses", () => {
  it("round-trips a pass-state address", () => {
    const a = {
      kind: "pass-state" as const,
      pass_id: "p_1",
      state_endpoint_url: "localhost:7081",
      session_token: "tok",
    };
    expect(decodeAddress(encodeAddress(a))).toEqual(a);
  });

  it("round-trips a source-tree-zone address", () => {
    const a = {
      kind: "source-tree-zone" as const,
      pass_id: "p_1",
      zone_id: "z_1",
      zone_label: "src/foo",
      zone_files: ["src/foo/a.ts", "src/foo/b.ts"],
      repo_root_path: "/host/repo",
      state_endpoint_url: "localhost:7081",
      session_token: "tok",
    };
    expect(decodeAddress(encodeAddress(a))).toEqual(a);
  });

  it("rejects malformed bytes", () => {
    expect(() => decodeAddress(new TextEncoder().encode("not-json"))).toThrow();
    expect(() =>
      decodeAddress(new TextEncoder().encode(JSON.stringify({ kind: "unknown" }))),
    ).toThrow();
  });

  it("pass-state schema validation", () => {
    expect(() =>
      PassStateAddressSchema.parse({ kind: "pass-state" }),
    ).toThrow();
  });

  it("source-tree-zone schema validation", () => {
    expect(() =>
      SourceTreeZoneAddressSchema.parse({ kind: "source-tree-zone", pass_id: "x" }),
    ).toThrow();
  });

  it("encodeScopeIdentity produces stable byte ordering", () => {
    const a = encodeScopeIdentity({
      kind: "source-tree-zone",
      pass_id: "p_1",
      zone_id: "z_1",
      zone_files: ["a.ts", "b.ts"],
    });
    const b = encodeScopeIdentity({
      kind: "source-tree-zone",
      zone_id: "z_1",
      pass_id: "p_1",
      zone_files: ["a.ts", "b.ts"],
    });
    expect(Buffer.from(a).equals(Buffer.from(b))).toBe(true);
  });
});
