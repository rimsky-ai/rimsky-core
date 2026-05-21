import { describe, it, expect } from "vitest";
import { TOOL_DEFINITIONS, TOOL_NAMES, ReviewFindingInput } from "./internal-mcp-tools.js";

describe("internal-mcp-tools", () => {
  it("declares the 10 review tools", () => {
    expect(TOOL_NAMES).toEqual([
      "review_context",
      "review_finding",
      "review_coverage",
      "review_complete",
      "review_run_tests",
      "review_commit_fix",
      "review_defer",
      "review_skip_zone",
      "review_request_help",
      "review_dedup_mark",
    ]);
    expect(TOOL_DEFINITIONS.every((d) => d.inputSchema)).toBe(true);
  });

  it("review_finding input validates a class-5b emission (no token field)", () => {
    expect(
      ReviewFindingInput.parse({
        class: "5b",
        file: "x.ts",
        description: "x",
        confidence: "high",
      }),
    ).toBeTruthy();
  });

  it("review_finding rejects bad class", () => {
    expect(() =>
      ReviewFindingInput.parse({ class: 7, file: "x", description: "x", confidence: "high" }),
    ).toThrow();
  });
});
