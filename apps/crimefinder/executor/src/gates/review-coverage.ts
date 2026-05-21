import type { GateContext } from "./types.js";

export async function reviewCoverage(
  input: { files_read: string[] },
  ctx: GateContext,
): Promise<{ recorded_count: number }> {
  return ctx.stateClient.appendCoverage(input);
}
