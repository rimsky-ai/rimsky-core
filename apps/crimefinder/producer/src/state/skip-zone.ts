import { generateRowId, makeGateError, GateError } from "@crimefinder/shared";
import { StateHandlerDeps, UnauthenticatedError } from "./handler-deps.js";

export interface SkipZoneRequest {
  session_token: string;
  reason: string;
}

export async function handleSkipZone(
  req: SkipZoneRequest,
  deps: StateHandlerDeps,
): Promise<{ zone_id: string; skipped: true }> {
  const meta = deps.tokens.validate(req.session_token);
  if (!meta) throw new UnauthenticatedError();
  if (!meta.zoneId || meta.role !== "review-zone") {
    throw new GateError(
      makeGateError(
        "wrong_session_role",
        "review_skip_zone requires a review-zone session bound to a zone",
        false,
        { actual_role: meta.role ?? null, required_role: "review-zone" },
      ),
    );
  }
  // Spec line 432: skip-zone requires coverage already below threshold —
  // otherwise the agent could short-circuit a reviewable zone by calling
  // skip on a fully-read zone. Reuse the same coverage logic
  // handleGetZoneCoverage computes so the gate and the threshold check
  // see the same number.
  const threshold = deps.config.coverage?.threshold_pct ?? 80;
  const zones = deps.partitionCache.getZonePlan(meta.passId) ?? [];
  const zone = zones.find((z) => z.id === meta.zoneId);
  const zoneFiles = new Set<string>(zone?.files ?? []);
  if (zoneFiles.size > 0) {
    const coverageRows = await deps.store.readCoverage();
    const readSet = new Set<string>();
    for (const row of coverageRows) {
      if (row.pass_id !== meta.passId) continue;
      if (row.session_id !== meta.claimHandleId) continue;
      if (!zoneFiles.has(row.file)) continue;
      readSet.add(row.file);
    }
    const pct = (readSet.size / zoneFiles.size) * 100;
    if (pct >= threshold) {
      throw new GateError(
        makeGateError(
          "coverage_above_threshold",
          `review_skip_zone refused: coverage ${pct.toFixed(1)}% already meets threshold ${threshold}%`,
          false,
          {
            coverage_pct: pct,
            threshold_pct: threshold,
            zone_id: meta.zoneId,
          },
        ),
      );
    }
  }
  await deps.store.appendSkipZone({
    kind: "skip_zone",
    id: generateRowId(),
    ts: new Date().toISOString(),
    pass_id: meta.passId,
    zone_id: meta.zoneId,
    session_id: meta.claimHandleId,
    reason: req.reason,
  });
  return { zone_id: meta.zoneId, skipped: true };
}
