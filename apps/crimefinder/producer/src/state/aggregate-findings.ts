import { StateHandlerDeps, UnauthenticatedError } from "./handler-deps.js";
import { materializeFindings } from "./materialize.js";

export interface AggregateFindingsRequest {
  session_token: string;
  pass_id: string;
}

export interface AggregateFindingsResponse {
  class_1_4_remaining: number;
  class_5_json: Uint8Array;
  dedup_file_groups_json: Uint8Array;
}

export async function handleAggregateFindings(
  req: AggregateFindingsRequest,
  deps: StateHandlerDeps,
): Promise<AggregateFindingsResponse> {
  const meta = deps.tokens.validate(req.session_token);
  if (!meta) throw new UnauthenticatedError();
  const passId = req.pass_id || meta.passId;
  const rows = await deps.store.readFindings();
  const m = materializeFindings(rows);
  let class14 = 0;
  const class5: unknown[] = [];
  const fileToIds = new Map<string, string[]>();
  for (const { row, status } of m.values()) {
    if (row.pass_id !== passId) continue;
    if (row.effective_class === "5a" || row.effective_class === "5b") class5.push(row);
    else if (status === "open" || status === "fixing") class14 += 1;
    if (status === "duplicate-of" || status === "void") continue;
    let b = fileToIds.get(row.file);
    if (!b) {
      b = [];
      fileToIds.set(row.file, b);
    }
    b.push(row.id);
  }
  const groups: Array<{ file: string; finding_ids: string[] }> = [];
  for (const [file, ids] of fileToIds) {
    if (ids.length >= 2) groups.push({ file, finding_ids: ids });
  }
  return {
    class_1_4_remaining: class14,
    class_5_json: new TextEncoder().encode(JSON.stringify(class5)),
    dedup_file_groups_json: new TextEncoder().encode(JSON.stringify(groups)),
  };
}
