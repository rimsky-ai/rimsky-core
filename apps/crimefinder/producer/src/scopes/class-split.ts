import type { OpenContext, OpenResult } from "./types.js";
import { parseSelectorQuery } from "./types.js";
import { materializeFindings } from "../state/materialize.js";

export async function openClassSplit(ctx: OpenContext): Promise<OpenResult> {
  const q = parseSelectorQuery(ctx.selector);
  const passId = q.pass_id;
  if (!passId) throw new Error("class-split selector missing pass_id");

  const rows = await ctx.store.readFindings();
  const m = materializeFindings(rows);

  let class14Remaining = false;
  const class5Findings: unknown[] = [];
  for (const { row, status } of m.values()) {
    if (row.pass_id !== passId) continue;
    if (status === "duplicate-of" || status === "void") continue;
    const cls = row.effective_class;
    if (cls === "5a" || cls === "5b") {
      class5Findings.push(row);
    } else if (status === "open" || status === "fixing") {
      class14Remaining = true;
    }
  }
  const payloadBytes = new TextEncoder().encode(
    JSON.stringify({ class_1_4_remaining: class14Remaining, class_5_findings: class5Findings }),
  );
  const scopeBytes = new TextEncoder().encode(
    JSON.stringify({ kind: "class-split", pass_id: passId }),
  );
  return { address: new Uint8Array(), payload: payloadBytes, scope: scopeBytes };
}
