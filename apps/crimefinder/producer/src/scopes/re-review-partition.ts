import { NoOpAddressSchema } from "@crimefinder/shared";
import type { OpenContext, OpenResult } from "./types.js";
import { parseSelectorQuery } from "./types.js";

// re-review-partition is a fan-out PARENT (like fix-partition / source-tree).
// All typed-state happens from the zone-scoped sub-claims SplitScope
// produces; the parent never calls a gate. Return a no-op address so no
// unbound session-token leaks into the parent's claim.
export async function openReReviewPartition(ctx: OpenContext): Promise<OpenResult> {
  const q = parseSelectorQuery(ctx.selector);
  const passId = q.pass_id;
  const iterNum = Number(q.iter_num);
  if (!passId || !Number.isFinite(iterNum)) {
    throw new Error("re-review-partition selector missing pass_id or iter_num");
  }
  const affected = ctx.partitionCache.getAffectedZones(passId, iterNum) ?? [];

  const address = NoOpAddressSchema.parse({
    kind: "no-op",
    pass_id: passId,
    note: "re-review-partition parent has no typed-state surface",
  });
  const addressBytes = new TextEncoder().encode(JSON.stringify(address));
  const payload = {
    iter_num: iterNum,
    affected_zones_count: affected.length,
    affected_zones: affected.map((z) => ({ id: z.id, label: z.label, files: z.files })),
  };
  const payloadBytes = new TextEncoder().encode(JSON.stringify(payload));
  const scopeBytes = new TextEncoder().encode(
    JSON.stringify({ kind: "re-review-partition", pass_id: passId, iter_num: iterNum }),
  );
  return { address: addressBytes, payload: payloadBytes, scope: scopeBytes };
}
