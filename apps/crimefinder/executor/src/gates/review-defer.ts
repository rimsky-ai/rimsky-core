import type { GateContext } from "./types.js";
import { event } from "./types.js";

export async function reviewDefer(
  input: { finding_id: string; reason: string },
  ctx: GateContext,
): Promise<{ finding_id: string; finding_status: "deferred" }> {
  const r = await ctx.stateClient.deferFinding(input);
  ctx.emit.emit(event(ctx, "finding_deferred", { finding_id: input.finding_id, reason: input.reason }));
  return r;
}
