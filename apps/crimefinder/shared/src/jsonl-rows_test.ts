import { describe, it, expect } from "vitest";
import {
  FindingRowSchema,
  FindingsRowSchema,
  StatusUpdateRowSchema,
  TensionConfirmationRowSchema,
  HelpRequestRowSchema,
  CoverageRowSchema,
  PassStartedRowSchema,
  PassFinishedRowSchema,
  IterMarkerRowSchema,
  PassesRowSchema,
  parseFindingsLine,
  serializeFindingsRow,
  parseCoverageLine,
  serializeCoverageRow,
  parsePassesLine,
  serializePassesRow,
} from "./jsonl-rows.js";

const NOW = "2026-05-19T12:34:56.000+00:00";

describe("jsonl-rows", () => {
  it("round-trips a finding row", () => {
    const row = {
      kind: "finding" as const,
      id: "f_abc",
      ts: NOW,
      pass_id: "p_x",
      zone_id: "z_y",
      session_id: "sess_z",
      class: 1 as const,
      effective_class: 1 as const,
      auto_rerouted: false,
      file: "src/foo.ts",
      line_start: 42,
      line_end: 47,
      symbol: "handleX",
      description: "bug here",
      fingerprint: "sha256:deadbeef",
      concept_slug: "claim-handle",
      tension_slug: null,
      confidence: "high" as const,
      status: "open" as const,
      originating_zone_id: null,
    };
    expect(FindingRowSchema.parse(row)).toEqual(row);
    const s = serializeFindingsRow(row);
    const parsed = parseFindingsLine(s);
    expect(parsed).toEqual(row);
  });

  it("round-trips a status_update row", () => {
    const row = {
      kind: "status_update" as const,
      id: "su_1",
      ts: NOW,
      ref: "f_abc",
      status: "fixed" as const,
      by_pass: "p_x",
      by_session: "sess_z",
      resolved_at_commit: "abc123",
    };
    expect(StatusUpdateRowSchema.parse(row)).toEqual(row);
  });

  it("round-trips a tension_confirmation row", () => {
    const row = {
      kind: "tension_confirmation" as const,
      id: "tc_1",
      ts: NOW,
      pass_id: "p_x",
      zone_id: "z_y",
      tension_slug: "callback-hostname-split",
      file: "src/foo.ts",
      description: "this is the tension",
    };
    expect(TensionConfirmationRowSchema.parse(row)).toEqual(row);
  });

  it("round-trips a help_request row", () => {
    const row = {
      kind: "help_request" as const,
      id: "hr_1",
      ts: NOW,
      pass_id: "p_x",
      session_id: "sess_z",
      question: "what does this mean?",
      blocker_finding_id: "f_abc",
      status: "open" as const,
    };
    expect(HelpRequestRowSchema.parse(row)).toEqual(row);
  });

  it("FindingsRowSchema discriminates on kind", () => {
    const finding = {
      kind: "finding" as const,
      id: "f_1",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_1",
      session_id: "sess_1",
      class: "5b" as const,
      effective_class: "5b" as const,
      auto_rerouted: true,
      file: "f",
      line_start: null,
      line_end: null,
      description: "x",
      fingerprint: "sha256:abc",
      concept_slug: null,
      tension_slug: null,
      confidence: "low" as const,
      status: "open" as const,
      originating_zone_id: null,
    };
    expect(FindingsRowSchema.parse(finding).kind).toBe("finding");
  });

  it("rejects bad enum values", () => {
    expect(() =>
      StatusUpdateRowSchema.parse({
        kind: "status_update",
        id: "x",
        ts: NOW,
        ref: "f_x",
        status: "not-a-status",
        by_pass: "p",
        by_session: "s",
      }),
    ).toThrow();
  });

  it("rejects missing required fields", () => {
    expect(() => FindingRowSchema.parse({ kind: "finding" })).toThrow();
  });

  it("round-trips coverage row", () => {
    const row = {
      ts: NOW,
      pass_id: "p_1",
      session_id: "s_1",
      zone_id: "z_1",
      file: "src/x.ts",
    };
    expect(CoverageRowSchema.parse(row)).toEqual(row);
    expect(parseCoverageLine(serializeCoverageRow(row))).toEqual(row);
  });

  it("round-trips pass_started + pass_finished + iter_marker via PassesRowSchema", () => {
    const started = {
      kind: "pass_started" as const,
      id: "p_1",
      ts: NOW,
      mission: "convergence pass",
      trigger: "manual" as const,
      template_hash: "sha256-abc",
      fix_cycle_cap: 3,
      params_hash: "sha256-def",
    };
    const finished = {
      kind: "pass_finished" as const,
      ref: "p_1",
      ts: NOW,
      exit_reason: "complete" as const,
      zones_planned: 12,
      zones_completed: 11,
      zones_skipped: 1,
      findings_emitted: 47,
      findings_resolved: 31,
      findings_deferred: 5,
      findings_class_5_remaining_open: 11,
      fix_cycle_iterations_run: 2,
      coverage_pct: 96.7,
      commits: ["abc123"],
    };
    const marker = {
      kind: "iter_marker" as const,
      id: "im_1",
      ts: NOW,
      pass_id: "p_1",
      iter_num: 2,
    };
    expect(PassStartedRowSchema.parse(started)).toEqual(started);
    expect(PassFinishedRowSchema.parse(finished)).toEqual(finished);
    expect(IterMarkerRowSchema.parse(marker)).toEqual(marker);
    expect(parsePassesLine(serializePassesRow(started)).kind).toBe("pass_started");
    expect(parsePassesLine(serializePassesRow(finished)).kind).toBe("pass_finished");
    expect(parsePassesLine(serializePassesRow(marker)).kind).toBe("iter_marker");
  });

  it("PassesRowSchema rejects unknown kinds", () => {
    expect(() => PassesRowSchema.parse({ kind: "nope" })).toThrow();
  });
});
