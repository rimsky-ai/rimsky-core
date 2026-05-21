import { PassFinishedRowSchema } from "@crimefinder/shared";
import type { OpenContext, OpenResult } from "./types.js";
import { parseSelectorQuery } from "./types.js";
import { materializeFindings } from "../state/materialize.js";

export async function openReport(ctx: OpenContext): Promise<OpenResult> {
  const q = parseSelectorQuery(ctx.selector);
  const passId = q.pass_id;
  if (!passId) throw new Error("report selector missing pass_id");

  const findingsRows = await ctx.store.readFindings();
  const coverageRows = await ctx.store.readCoverage();
  const passes = await ctx.store.readPasses();
  const m = materializeFindings(findingsRows);

  const zones = ctx.partitionCache.getZonePlan(passId) ?? [];
  const zonesPlanned = zones.length;

  let findingsEmitted = 0;
  let findingsResolved = 0;
  let findingsDeferred = 0;
  let class5RemainingOpen = 0;
  const commits = new Set<string>();
  const zonesCompletedSet = new Set<string>();

  for (const { row, status, lastUpdate } of m.values()) {
    if (row.pass_id !== passId) continue;
    findingsEmitted += 1;
    if (status === "fixed") {
      findingsResolved += 1;
      if (lastUpdate?.resolved_at_commit) commits.add(lastUpdate.resolved_at_commit);
    } else if (status === "deferred") {
      findingsDeferred += 1;
    }
    const cls = row.effective_class;
    if ((cls === "5a" || cls === "5b") && status !== "fixed" && status !== "deferred" && status !== "queued-to-spec" && status !== "resolved-via-spec") {
      class5RemainingOpen += 1;
    }
  }

  // Zones covered: any coverage row for this pass
  for (const cv of coverageRows) {
    if (cv.pass_id === passId) zonesCompletedSet.add(cv.zone_id);
  }
  const skipRows = passes
    .filter((p) => p.kind === "skip_zone" && p.pass_id === passId)
    .map((p) => (p.kind === "skip_zone" ? p : null))
    .filter((p): p is Extract<typeof passes[number], { kind: "skip_zone" }> => p !== null);
  const zonesSkipped = new Set(skipRows.map((p) => p.zone_id)).size;
  // Don't double-count: zones_completed excludes skipped.
  for (const r of skipRows) {
    zonesCompletedSet.delete(r.zone_id);
  }
  const zonesCompleted = zonesCompletedSet.size;

  // Coverage % across the pass:
  let coveredFileTotal = 0;
  let zoneFileTotal = 0;
  const coveredByZone = new Map<string, Set<string>>();
  for (const cv of coverageRows) {
    if (cv.pass_id !== passId) continue;
    let bucket = coveredByZone.get(cv.zone_id);
    if (!bucket) {
      bucket = new Set();
      coveredByZone.set(cv.zone_id, bucket);
    }
    bucket.add(cv.file);
  }
  for (const z of zones) {
    zoneFileTotal += z.files.length;
    const cov = coveredByZone.get(z.id);
    if (cov) {
      const zoneFiles = new Set(z.files);
      for (const f of cov) if (zoneFiles.has(f)) coveredFileTotal += 1;
    }
  }
  const coveragePct = zoneFileTotal > 0 ? (coveredFileTotal / zoneFileTotal) * 100 : 100;

  // Count iterations that actually did work — i.e. iterations whose
  // iter_marker timestamp is before at least one commit-status_update for
  // this pass. iter_markers exist for empty iterations too (the counter
  // advances regardless), so naively counting them would overstate
  // `fix_cycle_iterations_run`. Approximate by counting commits → each
  // commit happened inside some iter, and the count of distinct fixes
  // is a cleaner proxy for "iterations that did something" than marker
  // rows.
  const iterMarkers = passes.filter((p) => p.kind === "iter_marker" && p.pass_id === passId);
  // Walk findings to find iter_num via the commit's by_session... actually
  // we don't have iter_num on status_update. Use a simpler heuristic:
  // count iter_markers whose timestamp precedes at least one commit. Each
  // iter_marker.ts is the iteration-start time; if a `kind:"status_update"
  // status:"fixed"` exists with ts >= iter_marker.ts AND <
  // next_iter_marker.ts, that iteration did work.
  const sortedMarkers = iterMarkers
    .slice()
    .sort((a, b) => (a.kind === "iter_marker" && b.kind === "iter_marker" ? a.iter_num - b.iter_num : 0));
  const fixUpdates = findingsRows.filter(
    (r) => r.kind === "status_update" && r.status === "fixed" && r.by_pass === passId,
  );
  let fixIterationsRun = 0;
  for (let i = 0; i < sortedMarkers.length; i++) {
    const cur = sortedMarkers[i];
    const nxt = sortedMarkers[i + 1];
    if (cur.kind !== "iter_marker") continue;
    const startTs = cur.ts;
    const endTs = nxt && nxt.kind === "iter_marker" ? nxt.ts : null;
    const didWork = fixUpdates.some(
      (u) => u.ts >= startTs && (endTs === null || u.ts < endTs),
    );
    if (didWork) fixIterationsRun += 1;
  }

  const summary = PassFinishedRowSchema.parse({
    kind: "pass_finished",
    ref: passId,
    ts: new Date().toISOString(),
    exit_reason: "complete",
    zones_planned: zonesPlanned,
    zones_completed: zonesCompleted,
    zones_skipped: zonesSkipped,
    findings_emitted: findingsEmitted,
    findings_resolved: findingsResolved,
    findings_deferred: findingsDeferred,
    findings_class_5_remaining_open: class5RemainingOpen,
    fix_cycle_iterations_run: fixIterationsRun,
    coverage_pct: Math.round(coveragePct * 100) / 100,
    commits: [...commits],
  });
  await ctx.store.appendPassFinished(summary);

  const payloadBytes = new TextEncoder().encode(JSON.stringify(summary));
  const scopeBytes = new TextEncoder().encode(JSON.stringify({ kind: "report", pass_id: passId }));
  return { address: new Uint8Array(), payload: payloadBytes, scope: scopeBytes };
}
