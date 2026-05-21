import { NoOpAddressSchema } from "@crimefinder/shared";
import type { OpenContext, OpenResult } from "./types.js";
import { parseSelectorQuery } from "./types.js";

// fix-partition is a fan-out PARENT. Its zone-scoped sub-claims (issued via
// SplitScope → openFanOutChild) are what dispatch agents; the parent itself
// never calls typed-state. Mirroring openSourceTree, we return a no-op
// address so no unbound session-token leaks into the parent's claim. The
// payload carries `affected_zones` (so downstream consumers like
// iter-aggregate see the partition shape) and `iter_num`; per-zone
// finding IDs are computed inside `splitAffected` and travel on each
// child's address bytes — they do NOT belong on the parent's payload.
export async function openFixPartition(ctx: OpenContext): Promise<OpenResult> {
  const q = parseSelectorQuery(ctx.selector);
  const passId = q.pass_id;
  const iterNum = Number(q.iter_num);
  if (!passId || !Number.isFinite(iterNum)) {
    throw new Error("fix-partition selector missing pass_id or iter_num");
  }
  const affected = ctx.partitionCache.getAffectedZones(passId, iterNum) ?? [];

  const address = NoOpAddressSchema.parse({
    kind: "no-op",
    pass_id: passId,
    note: "fix-partition parent has no typed-state surface",
  });
  const addressBytes = new TextEncoder().encode(JSON.stringify(address));
  const payload = {
    iter_num: iterNum,
    affected_zones_count: affected.length,
    affected_zones: affected.map((z) => ({ id: z.id, label: z.label, files: z.files })),
  };
  const payloadBytes = new TextEncoder().encode(JSON.stringify(payload));
  const scopeBytes = new TextEncoder().encode(
    JSON.stringify({ kind: "fix-partition", pass_id: passId, iter_num: iterNum }),
  );
  return { address: addressBytes, payload: payloadBytes, scope: scopeBytes };
}
