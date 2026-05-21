import type { GateContext } from "./types.js";
import { event } from "./types.js";

export async function reviewRunTests(
  _input: unknown,
  ctx: GateContext,
): Promise<{ exit_code: number; output_excerpt: string; ran_at: string; cached: boolean }> {
  const r = await ctx.stateClient.runTests();
  ctx.emit.emit(event(ctx, "tests_ran", { exit_code: r.exit_code, cached: r.cached }));
  return r;
}
