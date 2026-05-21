import { StateHandlerDeps, UnauthenticatedError } from "./handler-deps.js";
import { computeZoneCoverage } from "../zones/coverage.js";
import { materializeFindings } from "./materialize.js";

export interface GetZoneCoverageRequest {
  session_token: string;
}

export interface PassSummary {
  zones_planned: number;
  zones_completed: number;
  zones_skipped: number;
  findings_emitted: number;
  findings_resolved: number;
  findings_deferred: number;
  coverage_pct: number;
}

export interface GetZoneCoverageResponse {
  zone_id: string;
  zone_file_count: number;
  files_covered: number;
  coverage_pct: number;
  skip_recorded: boolean;
  // True iff, after counting this session as "covered/skipped", every
  // zone in the pass meets the producer's coverage threshold (or has a
  // skip_zone row) AND no other completion has already emitted
  // `pass_closed` for this pass. The producer is the canonical emission
  // gate (via the `pass_closed_emitted` JSONL row written under the
  // passes-file mutex), so at most one caller observes the truthy flag
  // even under concurrent completions. The executor reads this off
  // review_complete's coverage check and emits the `pass_closed`
  // named-event on the terminal review-zone completion.
  pass_complete: boolean;
  // JSON-encoded `PassSummary` for the executor to stamp onto the
  // emitted `pass_closed` event. Empty Uint8Array when
  // `pass_complete:false` (mirrors the aggregate-findings *_json
  // convention so the proto wire and the in-process gate path share a
  // shape).
  pass_summary_json: Uint8Array;
}

// Returns the coverage stats for the session's bound zone, plus whether
// this session has recorded a skip_zone row. Used by review_complete to
// enforce the coverage_below_threshold gate.
//
// `pass_complete` applies the configured coverage threshold UNIFORMLY to
// every zone (via `computeZoneCoverage`), not just the calling session's.
// A zone with one file read out of twenty (5 %) is NOT counted as done.
// Under concurrent zone-completions the producer's
// `tryClaimPassClosedEmission` ensures at most one caller sees
// `pass_complete:true`, so consumers never receive duplicate
// `pass_closed` events.
export async function handleGetZoneCoverage(
  req: GetZoneCoverageRequest,
  deps: StateHandlerDeps,
): Promise<GetZoneCoverageResponse> {
  const meta = deps.tokens.validate(req.session_token);
  if (!meta) throw new UnauthenticatedError();
  if (!meta.zoneId) {
    return {
      zone_id: "",
      zone_file_count: 0,
      files_covered: 0,
      coverage_pct: 100,
      skip_recorded: false,
      pass_complete: false,
      pass_summary_json: new Uint8Array(),
    };
  }
  const zones = deps.partitionCache.getZonePlan(meta.passId) ?? [];
  const zone = zones.find((z) => z.id === meta.zoneId);
  const zoneFiles = new Set<string>(zone?.files ?? []);
  const coverageRows = await deps.store.readCoverage();
  const readSet = new Set<string>();
  for (const row of coverageRows) {
    if (row.pass_id !== meta.passId) continue;
    if (row.session_id !== meta.claimHandleId) continue;
    if (!zoneFiles.has(row.file)) continue;
    readSet.add(row.file);
  }
  const filesCovered = readSet.size;
  const pct = zoneFiles.size > 0 ? (filesCovered / zoneFiles.size) * 100 : 100;

  // Skip-zone is durable in passes.jsonl as kind:"skip_zone". Look for a
  // row by this session in this zone.
  const passes = await deps.store.readPasses();
  let skipRecorded = false;
  for (const p of passes) {
    if (p.kind !== "skip_zone") continue;
    if (p.pass_id !== meta.passId) continue;
    if (p.zone_id !== meta.zoneId) continue;
    if (p.session_id !== meta.claimHandleId) continue;
    skipRecorded = true;
    break;
  }

  // Apply the threshold UNIFORMLY to all zones via `computeZoneCoverage`,
  // not just the calling session's. Previously the check counted any
  // zone with at least one coverage row as "done", which let a zone
  // with 5 % coverage trigger a premature `pass_closed`.
  const threshold = deps.config.coverage?.threshold_pct ?? 80;
  const skippedZoneIds = new Set<string>();
  for (const p of passes) {
    if (p.kind !== "skip_zone") continue;
    if (p.pass_id !== meta.passId) continue;
    skippedZoneIds.add(p.zone_id);
  }
  let allDone = false;
  if (zones.length > 0) {
    const summaries = computeZoneCoverage(coverageRows, meta.passId, zones);
    // Pretend the calling zone is at-threshold (or skipped) for the
    // gate check so the requester doesn't have to wait for their own
    // already-committed-but-not-yet-readable row.
    allDone = summaries.every((s) => {
      if (s.zoneId === meta.zoneId) {
        return skipRecorded || pct >= threshold || s.coveragePercent >= threshold || skippedZoneIds.has(s.zoneId);
      }
      return s.coveragePercent >= threshold || skippedZoneIds.has(s.zoneId);
    });
  }

  // The producer is the canonical de-dup gate. Even if multiple zones
  // observe `allDone:true` concurrently, only the first writer claims
  // the emission and returns the truthy flag downstream — see
  // jsonl-store.tryClaimPassClosedEmission and the rationale on the
  // `PassClosedEmittedRow` schema.
  let passComplete = false;
  let passSummaryBytes = new Uint8Array();
  if (allDone) {
    const claimed = await deps.store.tryClaimPassClosedEmission(meta.passId);
    if (claimed) {
      passComplete = true;
      const findingsRows = await deps.store.readFindings();
      const summary = buildPassSummary({
        passId: meta.passId,
        coverageRows,
        findingsRows,
        zones,
        threshold,
        skippedZoneIds,
        currentZoneId: meta.zoneId,
        currentZonePct: pct,
        currentZoneSkipped: skipRecorded,
      });
      passSummaryBytes = new TextEncoder().encode(JSON.stringify(summary));
    }
  }

  return {
    zone_id: meta.zoneId,
    zone_file_count: zoneFiles.size,
    files_covered: filesCovered,
    coverage_pct: pct,
    skip_recorded: skipRecorded,
    pass_complete: passComplete,
    pass_summary_json: passSummaryBytes,
  };
}

interface BuildSummaryArgs {
  passId: string;
  coverageRows: Awaited<ReturnType<StateHandlerDeps["store"]["readCoverage"]>>;
  findingsRows: Awaited<ReturnType<StateHandlerDeps["store"]["readFindings"]>>;
  zones: NonNullable<ReturnType<StateHandlerDeps["partitionCache"]["getZonePlan"]>>;
  threshold: number;
  skippedZoneIds: Set<string>;
  currentZoneId: string;
  currentZonePct: number;
  currentZoneSkipped: boolean;
}

// Compose the data blob the executor stamps onto `pass_closed`. Mirrors
// (a subset of) PassFinishedRow so subscribers see the same shape they
// read off the canonical JSONL row. `zones_completed` is "covered above
// threshold AND not skipped"; `zones_skipped` is "has a skip_zone row".
function buildPassSummary(args: BuildSummaryArgs): PassSummary {
  const materialized = materializeFindings(args.findingsRows);
  let findingsEmitted = 0;
  let findingsResolved = 0;
  let findingsDeferred = 0;
  for (const { row, status } of materialized.values()) {
    if (row.pass_id !== args.passId) continue;
    findingsEmitted += 1;
    if (status === "fixed") findingsResolved += 1;
    else if (status === "deferred") findingsDeferred += 1;
  }

  // Apply uniform threshold to count zones_completed.
  const summaries = computeZoneCoverage(args.coverageRows, args.passId, args.zones);
  let zonesCompleted = 0;
  for (const s of summaries) {
    if (args.skippedZoneIds.has(s.zoneId)) continue;
    if (s.zoneId === args.currentZoneId) {
      if (args.currentZoneSkipped) continue;
      if (s.coveragePercent >= args.threshold || args.currentZonePct >= args.threshold) {
        zonesCompleted += 1;
      }
      continue;
    }
    if (s.coveragePercent >= args.threshold) zonesCompleted += 1;
  }

  // Pass-wide coverage % across all planned files.
  let coveredFileTotal = 0;
  let zoneFileTotal = 0;
  const coveredByZone = new Map<string, Set<string>>();
  for (const cv of args.coverageRows) {
    if (cv.pass_id !== args.passId) continue;
    let bucket = coveredByZone.get(cv.zone_id);
    if (!bucket) {
      bucket = new Set();
      coveredByZone.set(cv.zone_id, bucket);
    }
    bucket.add(cv.file);
  }
  for (const z of args.zones) {
    zoneFileTotal += z.files.length;
    const cov = coveredByZone.get(z.id);
    if (cov) {
      const zoneFiles = new Set(z.files);
      for (const f of cov) if (zoneFiles.has(f)) coveredFileTotal += 1;
    }
  }
  const coveragePct = zoneFileTotal > 0
    ? Math.round((coveredFileTotal / zoneFileTotal) * 10000) / 100
    : 100;

  return {
    zones_planned: args.zones.length,
    zones_completed: zonesCompleted,
    zones_skipped: args.skippedZoneIds.size,
    findings_emitted: findingsEmitted,
    findings_resolved: findingsResolved,
    findings_deferred: findingsDeferred,
    coverage_pct: coveragePct,
  };
}
