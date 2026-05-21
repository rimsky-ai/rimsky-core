import { GateError } from "@crimefinder/shared";
import type { GateContext } from "./types.js";
import { event } from "./types.js";

export async function reviewCommitFix(
  input: { finding_id: string; fix_description: string; commit_message: string },
  ctx: GateContext,
): Promise<{ commit_sha: string; finding_status: "fixed" }> {
  try {
    const r = await ctx.stateClient.commitFix(input);
    ctx.emit.emit(
      event(ctx, "finding_resolved", {
        finding_id: input.finding_id,
        commit_sha: r.commit_sha,
        iter_num: ctx.iterNum ?? null,
      }),
    );
    return r;
  } catch (e) {
    if (e instanceof GateError && e.envelope.data.crimefinder_error_class === "commit_failed") {
      ctx.emit.emit(
        event(ctx, "commit_failed", {
          finding_id: input.finding_id,
          stderr_excerpt: String(e.envelope.data.stderr_excerpt ?? ""),
        }),
      );
    }
    throw e;
  }
}
