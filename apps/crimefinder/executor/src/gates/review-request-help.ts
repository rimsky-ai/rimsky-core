import type { GateContext } from "./types.js";
import { event } from "./types.js";

export async function reviewRequestHelp(
  input: { question: string; blocker_finding_id?: string },
  ctx: GateContext,
): Promise<{ help_id: string }> {
  const r = await ctx.stateClient.requestHelp(input);
  ctx.emit.emit(event(ctx, "help_requested", { help_id: r.help_id, question: input.question }));
  return r;
}
