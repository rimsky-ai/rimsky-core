import { describe, it, expect } from "vitest";
import {
  ReviewFindingInputSchema,
  ReviewFindingOutputSchema,
  ReviewCoverageInputSchema,
  ReviewCoverageOutputSchema,
  ReviewCompleteOutputSchema,
  ReviewRunTestsOutputSchema,
  ReviewCommitFixInputSchema,
  ReviewCommitFixOutputSchema,
  ReviewDeferInputSchema,
  ReviewSkipZoneInputSchema,
  ReviewSkipZoneOutputSchema,
  ReviewRequestHelpInputSchema,
  ReviewContextOutputSchema,
} from "./gate-io.js";

describe("gate-io", () => {
  it("review_finding input round-trips happy path", () => {
    const input = {
      class: 1 as const,
      file: "src/foo.ts",
      line_start: 10,
      line_end: 12,
      description: "missing return",
      concept_slug: "claim-handle",
      tension_slug: null,
      confidence: "high" as const,
    };
    expect(ReviewFindingInputSchema.parse(input)).toEqual(input);
  });

  it("review_finding input rejects bad class", () => {
    expect(() =>
      ReviewFindingInputSchema.parse({
        class: 7,
        file: "x",
        description: "x",
        confidence: "high",
      }),
    ).toThrow();
  });

  it("review_finding output accepts 5b", () => {
    expect(
      ReviewFindingOutputSchema.parse({
        finding_id: "f_x",
        effective_class: "5b",
        auto_rerouted: true,
      }),
    ).toBeTruthy();
  });

  it("review_coverage input/output round-trip", () => {
    const input = { files_read: ["a.ts", "b.ts"] };
    expect(ReviewCoverageInputSchema.parse(input)).toEqual(input);
    expect(ReviewCoverageOutputSchema.parse({ recorded_count: 2 })).toBeTruthy();
  });

  it("review_complete output", () => {
    expect(
      ReviewCompleteOutputSchema.parse({ findings_recorded: 3, coverage_pct: 87.5 }),
    ).toBeTruthy();
  });

  it("review_run_tests output", () => {
    expect(
      ReviewRunTestsOutputSchema.parse({
        exit_code: 0,
        output_excerpt: "PASS",
        ran_at: "2026-05-19T12:00:00Z",
        cached: false,
      }),
    ).toBeTruthy();
  });

  it("review_commit_fix input/output", () => {
    expect(
      ReviewCommitFixInputSchema.parse({
        finding_id: "f_x",
        fix_description: "fixed",
        commit_message: "fix: bug",
      }),
    ).toBeTruthy();
    expect(
      ReviewCommitFixOutputSchema.parse({ commit_sha: "abc", finding_status: "fixed" }),
    ).toBeTruthy();
  });

  it("review_defer input", () => {
    expect(
      ReviewDeferInputSchema.parse({ finding_id: "f_x", reason: "blocked" }),
    ).toBeTruthy();
  });

  it("review_skip_zone input/output", () => {
    expect(ReviewSkipZoneInputSchema.parse({ reason: "no relevant files" })).toBeTruthy();
    expect(ReviewSkipZoneOutputSchema.parse({ zone_id: "z_x", skipped: true })).toBeTruthy();
    expect(() => ReviewSkipZoneOutputSchema.parse({ zone_id: "z_x", skipped: false })).toThrow();
  });

  it("review_request_help input", () => {
    expect(ReviewRequestHelpInputSchema.parse({ question: "?" })).toBeTruthy();
  });

  it("review_context output discriminates on role", () => {
    const ctx = ReviewContextOutputSchema.parse({
      role: "review-zone",
      pass_id: "p_1",
      zone_id: "z_1",
      zone_label: "src/foo",
      mission: "x",
      zone_files: ["src/foo/a.ts"],
      concept_docs: [],
      open_tensions: [],
      existing_findings_in_zone: [],
      finding_categories_help: "5-class scheme",
      ignore_patterns: ["node_modules"],
    });
    expect(ctx.role).toBe("review-zone");
  });
});
