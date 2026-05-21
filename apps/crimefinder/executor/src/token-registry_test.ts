import { describe, it, expect } from "vitest";
import { McpTokenRegistry } from "./token-registry.js";

describe("McpTokenRegistry", () => {
  it("issues and validates", () => {
    const r = new McpTokenRegistry();
    const t = r.issue("run_1");
    expect(r.validate(t)?.runId).toBe("run_1");
  });
  it("returns null for unknown tokens", () => {
    const r = new McpTokenRegistry();
    expect(r.validate("nope")).toBeNull();
  });
  it("revokes tokens", () => {
    const r = new McpTokenRegistry();
    const t = r.issue("run_1");
    r.revoke(t);
    expect(r.validate(t)).toBeNull();
  });
});
