import { describe, it, expect } from "vitest";
import { SessionTokenRegistry } from "./session-tokens.js";

describe("SessionTokenRegistry", () => {
  it("issues then validates returns metadata", () => {
    const reg = new SessionTokenRegistry();
    const t = reg.issue({ passId: "p_1", claimHandleId: "c_1", issuedAt: 0 });
    const meta = reg.validate(t);
    expect(meta?.passId).toBe("p_1");
  });

  it("revoke removes validation", () => {
    const reg = new SessionTokenRegistry();
    const t = reg.issue({ passId: "p_1", claimHandleId: "c_1", issuedAt: 0 });
    reg.revoke(t);
    expect(reg.validate(t)).toBeNull();
  });

  it("multiple tokens are isolated", () => {
    const reg = new SessionTokenRegistry();
    const a = reg.issue({ passId: "p_a", claimHandleId: "c_a", issuedAt: 0 });
    const b = reg.issue({ passId: "p_b", claimHandleId: "c_b", issuedAt: 0 });
    expect(reg.validate(a)?.passId).toBe("p_a");
    expect(reg.validate(b)?.passId).toBe("p_b");
  });

  it("revokeByClaim drops the token bound to the claim", () => {
    const reg = new SessionTokenRegistry();
    const t = reg.issue({ passId: "p_1", claimHandleId: "c_xyz", issuedAt: 0 });
    reg.revokeByClaim("c_xyz");
    expect(reg.validate(t)).toBeNull();
  });

  it("validate enforces TTL", () => {
    // Move time forward by injecting `now`. The registry stamps issue time
    // at issue() and validates against `now()`; once `now > issue+ttl` the
    // token must be rejected (and tombstoned).
    let nowMs = 100;
    const reg = new SessionTokenRegistry({
      ttlMs: 1000,
      now: () => nowMs,
    });
    const t = reg.issue({ passId: "p_ttl", claimHandleId: "c_ttl", issuedAt: 0 });
    expect(reg.validate(t)?.passId).toBe("p_ttl");
    nowMs = 99999;
    expect(reg.validate(t)).toBeNull();
    // A subsequent validate should also be null (tombstoned).
    expect(reg.validate(t)).toBeNull();
  });
});
