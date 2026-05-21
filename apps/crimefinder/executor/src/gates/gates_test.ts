import { describe, it, expect } from "vitest";
import { pino } from "pino";
import type { GateContext, NamedEventEmitter } from "./types.js";
import { reviewFinding } from "./review-finding.js";
import { reviewCoverage } from "./review-coverage.js";
import { reviewRunTests } from "./review-run-tests.js";
import { reviewCommitFix } from "./review-commit-fix.js";
import { reviewDefer } from "./review-defer.js";
import { reviewSkipZone } from "./review-skip-zone.js";
import { reviewRequestHelp } from "./review-request-help.js";
import { reviewContext } from "./review-context.js";
import { reviewComplete } from "./review-complete.js";
import { NamedEventEnvelope, GateError, makeGateError } from "@crimefinder/shared";

const logger = pino({ level: "silent" });

function fakeEmitter(): NamedEventEmitter & { events: NamedEventEnvelope[] } {
  const events: NamedEventEnvelope[] = [];
  return {
    events,
    emit(env) {
      events.push(env);
    },
  };
}

function makeCtx(role: GateContext["role"], stateClient: GateContext["stateClient"]): GateContext & { emit: ReturnType<typeof fakeEmitter> } {
  const emit = fakeEmitter();
  return {
    stateClient,
    emit,
    passId: "p_1",
    zoneId: "z_1",
    sessionId: "sess_1",
    role,
    logger,
    zoneLabel: "src",
    zoneFiles: ["src/a.ts"],
    mission: "convergence pass",
  };
}

function stub(overrides: Partial<GateContext["stateClient"]>): GateContext["stateClient"] {
  return {
    appendFinding: async () => ({
      finding_id: "f_x",
      effective_class: "1",
      auto_rerouted: false,
      tension_confirmation: false,
    }),
    queryFindings: async () => ({ findings: [] }),
    updateFindingStatus: async () => ({ success: true }),
    appendCoverage: async (r: { files_read: string[] }) => ({ recorded_count: r.files_read.length }),
    runTests: async () => ({ exit_code: 0, output_excerpt: "ok", ran_at: "t", cached: false }),
    commitFix: async () => ({ commit_sha: "abc", finding_status: "fixed" as const }),
    deferFinding: async () => ({ finding_id: "f_x", finding_status: "deferred" as const }),
    skipZone: async () => ({ zone_id: "z_1", skipped: true as const }),
    requestHelp: async () => ({ help_id: "h_1" }),
    aggregateFindings: async () => ({ class_1_4_remaining: 0, class_5: [], dedup_file_groups: [] }),
    getZoneCoverage: async () => ({
      zone_id: "z_1",
      zone_file_count: 1,
      files_covered: 1,
      coverage_pct: 100,
      skip_recorded: false,
      pass_complete: false,
      pass_summary: null,
    }),
    getReviewContext: async () => ({
      role: "review-zone",
      pass_id: "p_1",
      zone_id: "z_1",
      zone_label: "src",
      zone_files: ["src/a.ts"],
      mission: "convergence pass",
      existing_findings_in_zone: [],
      concept_docs: [],
      open_tensions: [],
      finding_categories_help: "help",
      ignore_patterns: [],
    }),
    markDuplicate: async () => ({ success: true }),
    close: () => undefined,
    ...overrides,
  } as GateContext["stateClient"];
}

const PERMISSIVE_COMPLETE_OPTS = {
  coverageThresholdPct: 80,
  coverageOnBelow: "require_skip" as const,
};

describe("gates", () => {
  it("review_finding emits finding_emitted and returns finding_id", async () => {
    const ctx = makeCtx("review-zone", stub({}));
    const r = await reviewFinding(
      { class: 1, file: "x.ts", description: "x", confidence: "high" },
      ctx,
    );
    expect(r.finding_id).toBe("f_x");
    expect(ctx.emit.events[0].event).toBe("finding_emitted");
  });

  it("review_finding surfaces concept_citation_missing on auto_rerouted", async () => {
    const ctx = makeCtx(
      "review-zone",
      stub({
        appendFinding: async () => ({
          finding_id: "f_x",
          effective_class: "5b",
          auto_rerouted: true,
          tension_confirmation: false,
        }),
      }),
    );
    const r = await reviewFinding(
      { class: 1, file: "x.ts", description: "x", confidence: "high", concept_slug: "y" },
      ctx,
    );
    // Array shape lets both class-5b reroute and tension-confirmation
    // signals coexist on the same call without one overwriting the other.
    expect(r.crimefinder_error_classes).toEqual(["concept_citation_missing"]);
  });

  it("review_coverage delegates", async () => {
    const ctx = makeCtx("review-zone", stub({}));
    const r = await reviewCoverage({ files_read: ["a.ts", "b.ts"] }, ctx);
    expect(r.recorded_count).toBe(2);
  });

  it("review_run_tests emits tests_ran", async () => {
    const ctx = makeCtx("fix-cycle", stub({}));
    await reviewRunTests({}, ctx);
    expect(ctx.emit.events[0].event).toBe("tests_ran");
  });

  it("review_commit_fix emits finding_resolved on success", async () => {
    const ctx = makeCtx("fix-cycle", stub({}));
    const r = await reviewCommitFix(
      { finding_id: "f_x", fix_description: "x", commit_message: "x" },
      ctx,
    );
    expect(r.commit_sha).toBe("abc");
    expect(ctx.emit.events[0].event).toBe("finding_resolved");
  });

  it("review_commit_fix emits commit_failed and re-throws", async () => {
    const ctx = makeCtx(
      "fix-cycle",
      stub({
        commitFix: async () => {
          throw new GateError(
            makeGateError("commit_failed", "git rejected", false, { stderr_excerpt: "x" }),
          );
        },
      }),
    );
    await expect(
      reviewCommitFix({ finding_id: "f_x", fix_description: "x", commit_message: "x" }, ctx),
    ).rejects.toBeInstanceOf(GateError);
    expect(ctx.emit.events[0].event).toBe("commit_failed");
  });

  it("review_defer / review_skip_zone / review_request_help emit corresponding events", async () => {
    const ctx1 = makeCtx("review-zone", stub({}));
    await reviewDefer({ finding_id: "f_x", reason: "x" }, ctx1);
    expect(ctx1.emit.events[0].event).toBe("finding_deferred");

    const ctx2 = makeCtx("review-zone", stub({}));
    await reviewSkipZone({ reason: "x" }, ctx2);
    expect(ctx2.emit.events[0].event).toBe("zone_skipped");

    const ctx3 = makeCtx("review-zone", stub({}));
    await reviewRequestHelp({ question: "?" }, ctx3);
    expect(ctx3.emit.events[0].event).toBe("help_requested");
  });

  it("review_context returns role-specific payload threaded from producer", async () => {
    const ctx = makeCtx(
      "review-zone",
      stub({
        getReviewContext: async () => ({
          role: "review-zone",
          pass_id: "p_1",
          zone_id: "z_1",
          zone_label: "src",
          zone_files: ["src/a.ts"],
          mission: "convergence pass",
          existing_findings_in_zone: [{ id: "f_x", file: "src/a.ts", class: 1, status: "open" }],
          concept_docs: [{ slug: "c", path: "concepts/c.md", content: "x" }],
          open_tensions: [],
          finding_categories_help: "help",
          ignore_patterns: ["vendor/"],
        }),
      }),
    );
    const r = await reviewContext({}, ctx) as Record<string, unknown>;
    expect(r.role).toBe("review-zone");
    expect((r.existing_findings_in_zone as unknown[]).length).toBe(1);
    expect((r.concept_docs as unknown[]).length).toBe(1);
    expect(r.finding_categories_help).toBe("help");
  });

  it("review_complete rejects when findings are still fixing", async () => {
    const ctx = makeCtx(
      "review-zone",
      stub({
        queryFindings: async (args: { status_filter?: string }) =>
          args.status_filter === "fixing" ? { findings: [{ id: "f_x" }] } : { findings: [] },
      }),
    );
    await expect(reviewComplete({}, ctx, PERMISSIVE_COMPLETE_OPTS)).rejects.toBeInstanceOf(GateError);
  });

  it("review_complete passes when nothing is in flight, emits zone_completed", async () => {
    const ctx = makeCtx("review-zone", stub({}));
    const r = await reviewComplete({}, ctx, PERMISSIVE_COMPLETE_OPTS);
    expect(r.findings_recorded).toBe(0);
    expect(ctx.emit.events[0].event).toBe("zone_completed");
  });

  it("review_complete rejects with coverage_below_threshold when below + no skip", async () => {
    const ctx = makeCtx(
      "review-zone",
      stub({
        getZoneCoverage: async () => ({
          zone_id: "z_1",
          zone_file_count: 10,
          files_covered: 2,
          coverage_pct: 20,
          skip_recorded: false,
          pass_complete: false,
          pass_summary: null,
        }),
      }),
    );
    let caught: unknown;
    try {
      await reviewComplete({}, ctx, PERMISSIVE_COMPLETE_OPTS);
    } catch (e) {
      caught = e;
    }
    expect(caught).toBeInstanceOf(GateError);
    expect((caught as GateError).envelope.data.crimefinder_error_class).toBe(
      "coverage_below_threshold",
    );
  });

  it("review_complete accepts when skip_zone was recorded for below-threshold coverage", async () => {
    const ctx = makeCtx(
      "review-zone",
      stub({
        getZoneCoverage: async () => ({
          zone_id: "z_1",
          zone_file_count: 10,
          files_covered: 2,
          coverage_pct: 20,
          skip_recorded: true,
          pass_complete: false,
          pass_summary: null,
        }),
      }),
    );
    const r = await reviewComplete({}, ctx, PERMISSIVE_COMPLETE_OPTS);
    expect(r.coverage_pct).toBe(20);
  });

  it("review_complete emits pass_closed when producer reports pass_complete", async () => {
    const ctx = makeCtx(
      "review-zone",
      stub({
        getZoneCoverage: async () => ({
          zone_id: "z_1",
          zone_file_count: 1,
          files_covered: 1,
          coverage_pct: 100,
          skip_recorded: false,
          pass_complete: true,
          pass_summary: null,
        }),
      }),
    );
    await reviewComplete({}, ctx, PERMISSIVE_COMPLETE_OPTS);
    const names = ctx.emit.events.map((e) => e.event);
    expect(names).toContain("zone_completed");
    expect(names).toContain("pass_closed");
  });

  it("review_complete stamps pass_summary fields onto pass_closed data", async () => {
    const ctx = makeCtx(
      "review-zone",
      stub({
        getZoneCoverage: async () => ({
          zone_id: "z_1",
          zone_file_count: 1,
          files_covered: 1,
          coverage_pct: 100,
          skip_recorded: false,
          pass_complete: true,
          pass_summary: {
            zones_planned: 3,
            zones_completed: 2,
            zones_skipped: 1,
            findings_emitted: 7,
            findings_resolved: 4,
            findings_deferred: 1,
            coverage_pct: 92.5,
          },
        }),
      }),
    );
    await reviewComplete({}, ctx, PERMISSIVE_COMPLETE_OPTS);
    const closed = ctx.emit.events.find((e) => e.event === "pass_closed");
    expect(closed).toBeDefined();
    expect(closed!.data.exit_reason).toBe("complete");
    expect(closed!.data.zones_planned).toBe(3);
    expect(closed!.data.zones_completed).toBe(2);
    expect(closed!.data.zones_skipped).toBe(1);
    expect(closed!.data.findings_emitted).toBe(7);
    expect(closed!.data.findings_resolved).toBe(4);
    expect(closed!.data.findings_deferred).toBe(1);
    expect(closed!.data.coverage_pct).toBe(92.5);
  });

  it("review_complete warns but accepts when coverage_on_below=warn", async () => {
    const ctx = makeCtx(
      "review-zone",
      stub({
        getZoneCoverage: async () => ({
          zone_id: "z_1",
          zone_file_count: 10,
          files_covered: 5,
          coverage_pct: 50,
          skip_recorded: false,
          pass_complete: false,
          pass_summary: null,
        }),
      }),
    );
    const r = await reviewComplete({}, ctx, {
      coverageThresholdPct: 80,
      coverageOnBelow: "warn",
    });
    expect(r.coverage_pct).toBe(50);
  });
});
