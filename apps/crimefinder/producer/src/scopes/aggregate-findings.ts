import type { OpenContext, OpenResult } from "./types.js";
import { parseSelectorQuery } from "./types.js";
import { materializeFindings } from "../state/materialize.js";

export async function openAggregateFindings(ctx: OpenContext): Promise<OpenResult> {
  const q = parseSelectorQuery(ctx.selector);
  const passId = q.pass_id;
  if (!passId) throw new Error("aggregate-findings selector missing pass_id");

  const allRows = await ctx.store.readFindings();
  const materialized = materializeFindings(allRows);

  let class14Remaining = 0;
  const class5: unknown[] = [];
  // Track only files seen at least twice. First sighting parks the id in
  // `seenOnce`; the second sighting promotes the file to `seenMulti`
  // and subsequent sightings append to the bucket. Avoids growing a
  // full per-file map under large finding counts when only multi-finding
  // files appear in the output.
  const seenOnce = new Map<string, string>();
  const seenMulti = new Map<string, string[]>();
  for (const { row, status } of materialized.values()) {
    if (row.pass_id !== passId) continue;
    const cls = row.effective_class;
    if (cls === "5a" || cls === "5b") {
      class5.push(row);
    } else if (status === "open" || status === "fixing") {
      class14Remaining += 1;
    }
    if (status === "duplicate-of" || status === "void") continue;
    const existingMulti = seenMulti.get(row.file);
    if (existingMulti) {
      existingMulti.push(row.id);
      continue;
    }
    const firstId = seenOnce.get(row.file);
    if (firstId !== undefined) {
      seenOnce.delete(row.file);
      seenMulti.set(row.file, [firstId, row.id]);
      continue;
    }
    seenOnce.set(row.file, row.id);
  }
  const dedupFileGroups: Array<{ file: string; finding_ids: string[] }> = [];
  for (const [file, ids] of seenMulti) {
    dedupFileGroups.push({ file, finding_ids: ids });
  }

  const payload = {
    class_1_4_remaining: class14Remaining,
    class_5: class5,
    dedup_file_groups: dedupFileGroups,
  };
  const payloadBytes = new TextEncoder().encode(JSON.stringify(payload));
  const scopeBytes = new TextEncoder().encode(
    JSON.stringify({ kind: "aggregate-findings", pass_id: passId }),
  );
  return { address: new Uint8Array(), payload: payloadBytes, scope: scopeBytes };
}
