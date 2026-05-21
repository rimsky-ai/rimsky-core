import { GateError, makeGateError } from "@crimefinder/shared";
import type { GateContext } from "./types.js";
import { event } from "./types.js";

export interface ReviewCompleteOptions {
  // Coverage threshold the producer enforces; pulled from cfg:coverage.
  // The executor learns this at dispatch time via userdata and threads
  // it into GateContext so the gate can decide without re-reading config.
  coverageThresholdPct: number;
  coverageOnBelow: "require_skip" | "warn" | "allow";
}

export async function reviewComplete(
  _input: unknown,
  ctx: GateContext,
  opts: ReviewCompleteOptions,
): Promise<{ findings_recorded: number; coverage_pct: number }> {
  // Any findings still "fixing" for this session block completion.
  const inFlight = await ctx.stateClient.queryFindings({
    pass_id: ctx.passId,
    zone_id: ctx.zoneId,
    status_filter: "fixing",
  });
  if (inFlight.findings.length > 0) {
    throw new GateError(
      makeGateError(
        "unresolved_findings_in_flight",
        `${inFlight.findings.length} findings still in fixing state`,
        false,
        { count: inFlight.findings.length },
      ),
    );
  }

  // Coverage enforcement (spec lines 434, 1275-1278): compute the
  // session's coverage from the producer; reject if below threshold and
  // skip_zone wasn't recorded. Only enforced for zone-bound sessions
  // (review-zone role). fix-cycle/dedup/re-review have no zone-coverage
  // notion.
  let coveragePct = 100;
  let passComplete = false;
  let passSummary: Record<string, unknown> | null = null;
  if (ctx.zoneId && ctx.role === "review-zone") {
    const cov = await ctx.stateClient.getZoneCoverage();
    coveragePct = cov.coverage_pct;
    passComplete = cov.pass_complete;
    passSummary = cov.pass_summary;
    const below = cov.coverage_pct < opts.coverageThresholdPct;
    if (below && opts.coverageOnBelow === "require_skip" && !cov.skip_recorded) {
      throw new GateError(
        makeGateError(
          "coverage_below_threshold",
          `coverage ${cov.coverage_pct.toFixed(1)}% below threshold ${opts.coverageThresholdPct}%; call review_skip_zone or read more files`,
          false,
          {
            zone_id: cov.zone_id,
            zone_file_count: cov.zone_file_count,
            files_covered: cov.files_covered,
            coverage_pct: cov.coverage_pct,
            threshold_pct: opts.coverageThresholdPct,
          },
        ),
      );
    }
    if (below && opts.coverageOnBelow === "warn") {
      ctx.logger.warn(
        {
          zone_id: cov.zone_id,
          coverage_pct: cov.coverage_pct,
          threshold_pct: opts.coverageThresholdPct,
        },
        "coverage_below_threshold_warn",
      );
    }
  }

  const all = await ctx.stateClient.queryFindings({
    pass_id: ctx.passId,
    zone_id: ctx.zoneId,
  });
  ctx.emit.emit(
    event(ctx, "zone_completed", {
      findings_recorded: all.findings.length,
      coverage_pct: coveragePct,
    }),
  );
  // Spec lines 584-595: `pass_closed` is one of the twelve declared
  // named events. The producer's `getZoneCoverage` reports
  // `pass_complete:true` exactly once per pass (de-dup via the
  // `pass_closed_emitted` JSONL row, written under the passes-file
  // mutex), so this emission fires at most once even under concurrent
  // zone completions. The producer-supplied `pass_summary` is stamped
  // onto the event's `data` blob so subscribers see the same shape the
  // canonical `pass_finished` JSONL row carries.
  if (passComplete) {
    const data: Record<string, unknown> = { exit_reason: "complete" };
    if (passSummary) {
      data.zones_planned = passSummary.zones_planned;
      data.zones_completed = passSummary.zones_completed;
      data.zones_skipped = passSummary.zones_skipped;
      data.findings_emitted = passSummary.findings_emitted;
      data.findings_resolved = passSummary.findings_resolved;
      data.findings_deferred = passSummary.findings_deferred;
      data.coverage_pct = passSummary.coverage_pct;
    }
    ctx.emit.emit(event(ctx, "pass_closed", data));
  }
  return { findings_recorded: all.findings.length, coverage_pct: coveragePct };
}
