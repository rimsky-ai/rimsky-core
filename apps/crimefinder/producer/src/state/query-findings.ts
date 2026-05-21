import { StateHandlerDeps, UnauthenticatedError } from "./handler-deps.js";
import { materializeFindings } from "./materialize.js";

export interface QueryFindingsRequest {
  session_token: string;
  pass_id?: string;
  zone_id?: string;
  status_filter?: string;
}

export interface QueryFindingsResponse {
  findings_json: Uint8Array;
}

export async function handleQueryFindings(
  req: QueryFindingsRequest,
  deps: StateHandlerDeps,
): Promise<QueryFindingsResponse> {
  const meta = deps.tokens.validate(req.session_token);
  if (!meta) throw new UnauthenticatedError();
  const passId = req.pass_id || meta.passId;
  const allRows = await deps.store.readFindings();
  const materialized = materializeFindings(allRows);
  const out = [];
  for (const { row, status } of materialized.values()) {
    if (row.pass_id !== passId) continue;
    if (req.zone_id && row.zone_id !== req.zone_id) continue;
    if (req.status_filter && status !== req.status_filter) continue;
    out.push({ ...row, status });
  }
  return { findings_json: new TextEncoder().encode(JSON.stringify(out)) };
}
