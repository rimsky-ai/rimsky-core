import { NoOpAddressSchema, generateRowId } from "@crimefinder/shared";
import type { OpenContext, OpenResult } from "./types.js";
import { parseSelectorQuery } from "./types.js";
import { groupFindingsByFile, batchFileGroups } from "../dedup/group.js";

// dedup-grouping is the fan-out PARENT for the dedup pass. It computes the
// per-batch file groups (cached for SplitScope) but never calls typed-state
// itself — all gate traffic comes from the dedup-batch sub-claims. So no
// session-token is issued, and the address is a no-op (mirrors source-tree
// / fix-partition / re-review-partition).
export async function openDedupGrouping(ctx: OpenContext): Promise<OpenResult> {
  const q = parseSelectorQuery(ctx.selector);
  const passId = q.pass_id;
  if (!passId) throw new Error("dedup-grouping selector missing pass_id");

  // Prefer cache if already populated for this pass — both for re-Open
  // within one producer process and for restart recovery (the startup
  // scan rehydrates the cache from the persisted dedup_batches row). Only
  // recompute when the cache is empty so a producer crash mid-dedup keeps
  // sub-claims pointing at the same batches the original Open computed.
  let batchesFileGroups = ctx.partitionCache.getDedupBatches(passId);
  if (!batchesFileGroups) {
    const rows = await ctx.store.readFindings();
    const findings = rows.filter((r) => r.kind === "finding" && r.pass_id === passId);
    const groups = groupFindingsByFile(findings.map((f) => f as never) as never);
    const batches = batchFileGroups(groups, findings as never);
    batchesFileGroups = batches.map((b) => b.fileGroups);
    ctx.partitionCache.setDedupBatches(passId, batchesFileGroups);
    // Persist alongside the zone_plan row so a producer crash mid-pass
    // can rehydrate the SAME batch layout post-restart. Without this,
    // dedup-batch sub-claims surviving a restart would resolve their
    // `getReviewContext` payload with an empty `file_groups`.
    await ctx.store.appendDedupBatches({
      kind: "dedup_batches",
      id: generateRowId(),
      ts: new Date().toISOString(),
      pass_id: passId,
      batches: batchesFileGroups.map((batch) =>
        batch.map((g) => ({ file: g.file, finding_ids: g.findingIds })),
      ),
    });
  }

  const address = NoOpAddressSchema.parse({
    kind: "no-op",
    pass_id: passId,
    note: "dedup-grouping parent has no typed-state surface",
  });
  const addressBytes = new TextEncoder().encode(JSON.stringify(address));
  const payloadBytes = new TextEncoder().encode(
    JSON.stringify({ batch_count: batchesFileGroups.length }),
  );
  const scopeBytes = new TextEncoder().encode(
    JSON.stringify({ kind: "dedup-grouping", pass_id: passId }),
  );
  return { address: addressBytes, payload: payloadBytes, scope: scopeBytes };
}
