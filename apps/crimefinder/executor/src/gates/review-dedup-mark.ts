import type { GateContext } from "./types.js";
import { event } from "./types.js";

// review_dedup_mark: dedup-role-only gate that flags one finding as a
// duplicate of another. The producer enforces the role check and writes
// the status_update row. Emits `finding_dedup_marked` per spec line 586 so
// observability consumers can track dedup decisions.
export async function reviewDedupMark(
  input: { finding_id: string; duplicate_of: string },
  ctx: GateContext,
): Promise<{ success: boolean; skipped_due_to_conflict?: boolean }> {
  const r = await ctx.stateClient.markDuplicate(input);
  ctx.emit.emit(
    event(ctx, "finding_dedup_marked", {
      finding_id: input.finding_id,
      duplicate_of: input.duplicate_of,
      skipped_due_to_conflict: r.skipped_due_to_conflict ?? false,
    }),
  );
  return r;
}
