import type { GateContext } from "./types.js";

// review_context returns the role-polymorphic ContextPayload from the
// producer's GetReviewContext RPC. Per spec lines 436-477, the payload
// includes concept_docs, open_tensions, finding_categories_help,
// ignore_patterns (review-zone role) and assigned_findings, test_command,
// require_tests_before_commit (fix-cycle role). We rely on the producer
// to assemble all that — the gate is a thin pass-through that overlays
// the executor's role/zone hints onto the JSON the producer returned.
export interface ReviewContextResult {
  role: GateContext["role"];
  [k: string]: unknown;
}

export async function reviewContext(_input: unknown, ctx: GateContext): Promise<ReviewContextResult> {
  const assignedIds = (ctx.assignedFindingIds ?? []).slice();
  const payload = (await ctx.stateClient.getReviewContext({
    assigned_finding_ids: assignedIds,
  })) as Record<string, unknown>;
  // Stamp the executor's view of role/mission/zone onto the payload so a
  // consumer that ignores producer-supplied fields still sees them.
  return {
    role: ctx.role,
    mission: ctx.mission ?? payload.mission,
    zone_id: ctx.zoneId ?? payload.zone_id,
    zone_label: ctx.zoneLabel ?? payload.zone_label,
    zone_files: ctx.zoneFiles ?? payload.zone_files,
    ...payload,
  };
}
