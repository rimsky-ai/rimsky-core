import type { OpenContext, OpenResult } from "./types.js";
import { parseSelectorQuery } from "./types.js";
import { materializeFindings } from "../state/materialize.js";
import { mapFileToZone } from "../zones/coverage.js";
import type { Zone } from "../zones/partition.js";

export async function openUnresolvedClass14(ctx: OpenContext): Promise<OpenResult> {
  const q = parseSelectorQuery(ctx.selector);
  const passId = q.pass_id;
  if (!passId) throw new Error("unresolved-class-1-4 selector missing pass_id");

  const rows = await ctx.store.readFindings();
  const m = materializeFindings(rows);
  const zones = ctx.partitionCache.getZonePlan(passId) ?? [];

  const affectedById = new Map<string, Zone>();
  for (const { row, status } of m.values()) {
    if (row.pass_id !== passId) continue;
    const cls = row.effective_class;
    if (cls === "5a" || cls === "5b") continue;
    if (status !== "open" && status !== "fixing") continue;
    const zone = mapFileToZone(row.file, zones);
    if (zone) affectedById.set(zone.id, zone);
  }

  const affectedZones = [...affectedById.values()];
  const skipped = affectedZones.length === 0;
  // Threading the claimId in makes nextFor idempotent: if rimsky retries
  // this Open (transient network failure), we return the iter_num issued
  // on the first attempt instead of bumping the counter again.
  // The counter advances even on skipped iterations so consumers (e.g. the
  // template's fix-iter-N nodes) see a monotonically-increasing iter_num
  // even if a downstream iter is a no-op. `fix_cycle_iterations_run` in the
  // pass report is computed from commits, not iter_marker count, so empty
  // iterations don't inflate it (see scopes/report.ts).
  const iterNum = await ctx.iterCounter.nextFor(passId, ctx.claimId);
  if (!skipped) {
    ctx.partitionCache.setAffectedZones(passId, iterNum, affectedZones);
  }

  const payload = {
    iter_num: iterNum,
    affected_zones: affectedZones.map((z) => z.id),
    affected_zones_data: affectedZones,
    skipped,
  };
  const payloadBytes = new TextEncoder().encode(JSON.stringify(payload));
  const scopeBytes = new TextEncoder().encode(
    JSON.stringify({ kind: "unresolved-class-1-4", pass_id: passId, iter_num: iterNum }),
  );
  return { address: new Uint8Array(), payload: payloadBytes, scope: scopeBytes };
}
