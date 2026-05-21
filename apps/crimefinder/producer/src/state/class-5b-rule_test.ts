import { describe, it, expect } from "vitest";
import { shouldRerouteToClass5b } from "./class-5b-rule.js";

describe("shouldRerouteToClass5b", () => {
  it("does NOT reroute when description quotes 8+ consecutive tokens from boundaries", () => {
    const boundaries = "the claim handle does not perform any side effecting filesystem operations";
    const desc = "see issue, the claim handle does not perform any side effecting filesystem operations here";
    expect(
      shouldRerouteToClass5b({ description: desc, conceptBoundaries: boundaries, conceptInvariants: "" }),
    ).toBe(false);
  });

  it("reroutes when only 7 consecutive tokens match (below threshold)", () => {
    // 7 useful tokens together — should reroute.
    const boundaries = "claim handle does perform side filesystem operations heavy";
    const desc = "the claim handle does perform side filesystem operations";
    expect(
      shouldRerouteToClass5b({
        description: desc,
        conceptBoundaries: boundaries,
        conceptInvariants: "",
        minTokenRun: 8,
      }),
    ).toBe(true);
  });

  it("reroutes when matching tokens appear in a different order", () => {
    const boundaries = "alpha beta gamma delta epsilon zeta eta theta";
    const desc = "theta eta zeta epsilon delta gamma beta alpha";
    expect(
      shouldRerouteToClass5b({ description: desc, conceptBoundaries: boundaries, conceptInvariants: "" }),
    ).toBe(true);
  });

  it("reroutes when both boundaries and invariants are empty", () => {
    expect(
      shouldRerouteToClass5b({
        description: "any words here whatsoever",
        conceptBoundaries: "",
        conceptInvariants: "",
      }),
    ).toBe(true);
  });

  it("matches case-insensitively", () => {
    const boundaries = "alpha beta gamma delta epsilon zeta eta theta iota kappa";
    const desc = "PREFIX ALPHA BETA GAMMA DELTA EPSILON ZETA ETA THETA IOTA KAPPA suffix";
    expect(
      shouldRerouteToClass5b({ description: desc, conceptBoundaries: boundaries, conceptInvariants: "" }),
    ).toBe(false);
  });

  it("checks invariants when boundaries is empty", () => {
    const invariants = "claim handle must always release before commit completes successfully and cleanly always";
    const desc = "the claim handle must always release before commit completes successfully and cleanly always";
    expect(
      shouldRerouteToClass5b({ description: desc, conceptBoundaries: "", conceptInvariants: invariants }),
    ).toBe(false);
  });
});
