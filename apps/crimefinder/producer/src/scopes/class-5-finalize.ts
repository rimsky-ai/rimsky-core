import type { OpenContext, OpenResult } from "./types.js";
import { parseSelectorQuery } from "./types.js";
import { materializeFindings } from "../state/materialize.js";

export async function openClass5Finalize(ctx: OpenContext): Promise<OpenResult> {
  const q = parseSelectorQuery(ctx.selector);
  const passId = q.pass_id;
  if (!passId) throw new Error("class-5-finalize selector missing pass_id");

  const rows = await ctx.store.readFindings();
  const m = materializeFindings(rows);
  let open = 0;
  let resolved = 0;
  let deferred = 0;
  let queuedToSpec = 0;
  for (const { row, status } of m.values()) {
    if (row.pass_id !== passId) continue;
    const cls = row.effective_class;
    if (cls !== "5a" && cls !== "5b") continue;
    if (status === "fixed" || status === "resolved-via-spec") resolved += 1;
    else if (status === "deferred") deferred += 1;
    else if (status === "queued-to-spec") queuedToSpec += 1;
    else open += 1;
  }
  const payload = {
    class_5_open: open,
    class_5_resolved: resolved,
    class_5_deferred: deferred,
    class_5_queued_to_spec: queuedToSpec,
  };
  const payloadBytes = new TextEncoder().encode(JSON.stringify(payload));
  const scopeBytes = new TextEncoder().encode(
    JSON.stringify({ kind: "class-5-finalize", pass_id: passId }),
  );
  return { address: new Uint8Array(), payload: payloadBytes, scope: scopeBytes };
}
