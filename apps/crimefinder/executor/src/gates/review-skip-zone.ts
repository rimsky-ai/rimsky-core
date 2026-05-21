import type { GateContext } from "./types.js";
import { event } from "./types.js";

export async function reviewSkipZone(
  input: { reason: string },
  ctx: GateContext,
): Promise<{ zone_id: string; skipped: true }> {
  const r = await ctx.stateClient.skipZone(input);
  ctx.emit.emit(event(ctx, "zone_skipped", { reason: input.reason }));
  return r;
}
