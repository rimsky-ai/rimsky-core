import type { OpenContext, OpenResult } from "./types.js";
import { parseSelectorQuery } from "./types.js";
import { materializeFindings } from "../state/materialize.js";

export async function openIterAggregate(ctx: OpenContext): Promise<OpenResult> {
  const q = parseSelectorQuery(ctx.selector);
  const passId = q.pass_id;
  const iterNum = Number(q.iter_num);
  if (!passId || !Number.isFinite(iterNum)) {
    throw new Error("iter-aggregate selector missing pass_id or iter_num");
  }

  // Walk passes.jsonl iter_marker rows to find the timestamp window for
  // this iter. Window is [marker_for_iter_N, marker_for_iter_N+1) — fixes
  // resolved inside that window count toward `findings_resolved_this_iter`.
  const passes = await ctx.store.readPasses();
  const sortedMarkers = passes
    .filter((p) => p.kind === "iter_marker" && p.pass_id === passId)
    .map((p) => (p.kind === "iter_marker" ? p : null))
    .filter((p): p is Extract<typeof passes[number], { kind: "iter_marker" }> => p !== null)
    .sort((a, b) => a.iter_num - b.iter_num);
  const thisMarker = sortedMarkers.find((m) => m.iter_num === iterNum);
  const nextMarker = sortedMarkers.find((m) => m.iter_num === iterNum + 1);
  const windowStart = thisMarker?.ts ?? null;
  const windowEnd = nextMarker?.ts ?? null;

  const rows = await ctx.store.readFindings();
  const m = materializeFindings(rows);
  let moreWorkNeeded = false;
  let resolvedThisIter = 0;

  for (const { row, status, lastUpdate } of m.values()) {
    if (row.pass_id !== passId) continue;
    const cls = row.effective_class;
    if (cls !== "5a" && cls !== "5b") {
      if (status === "open" || status === "fixing") moreWorkNeeded = true;
      if (
        status === "fixed" &&
        lastUpdate &&
        lastUpdate.by_pass === passId &&
        windowStart !== null &&
        lastUpdate.ts >= windowStart &&
        (windowEnd === null || lastUpdate.ts < windowEnd)
      ) {
        resolvedThisIter += 1;
      }
    }
  }

  const payload = { more_work_needed: moreWorkNeeded, findings_resolved_this_iter: resolvedThisIter };
  const payloadBytes = new TextEncoder().encode(JSON.stringify(payload));
  const scopeBytes = new TextEncoder().encode(
    JSON.stringify({ kind: "iter-aggregate", pass_id: passId, iter_num: iterNum }),
  );
  return { address: new Uint8Array(), payload: payloadBytes, scope: scopeBytes };
}
