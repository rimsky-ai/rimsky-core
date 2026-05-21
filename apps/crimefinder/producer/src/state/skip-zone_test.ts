import { describe, it, expect } from "vitest";
import { makeStateDeps } from "./test-helpers.js";
import { handleSkipZone } from "./skip-zone.js";

describe("handleSkipZone", () => {
  it("records a skip_zone row", async () => {
    const { deps } = await makeStateDeps();
    const tok = deps.tokens.issue({
      passId: "p",
      claimHandleId: "s",
      zoneId: "z_x",
      role: "review-zone",
      issuedAt: 0,
    });
    const r = await handleSkipZone({ session_token: tok, reason: "nothing here" }, deps);
    expect(r.skipped).toBe(true);
    expect(r.zone_id).toBe("z_x");
    const passes = await deps.store.readPasses();
    expect(passes.some((p) => p.kind === "skip_zone")).toBe(true);
  });

  it("rejects when session has no zone", async () => {
    const { deps } = await makeStateDeps();
    const tok = deps.tokens.issue({
      passId: "p",
      claimHandleId: "s",
      role: "review-zone",
      issuedAt: 0,
    });
    await expect(handleSkipZone({ session_token: tok, reason: "x" }, deps)).rejects.toThrow();
  });

  it("rejects when session role is not review-zone", async () => {
    const { deps } = await makeStateDeps();
    const tok = deps.tokens.issue({
      passId: "p",
      claimHandleId: "s",
      zoneId: "z_x",
      role: "fix-cycle",
      issuedAt: 0,
    });
    await expect(handleSkipZone({ session_token: tok, reason: "x" }, deps)).rejects.toThrow();
  });
});
