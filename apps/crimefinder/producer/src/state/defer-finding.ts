import { generateRowId, makeGateError, GateError } from "@crimefinder/shared";
import { StateHandlerDeps, UnauthenticatedError } from "./handler-deps.js";
import { materializeFindings } from "./materialize.js";

export interface DeferFindingRequest {
  session_token: string;
  finding_id: string;
  reason: string;
}

export async function handleDeferFinding(
  req: DeferFindingRequest,
  deps: StateHandlerDeps,
): Promise<{ finding_id: string; finding_status: "deferred" }> {
  const meta = deps.tokens.validate(req.session_token);
  if (!meta) throw new UnauthenticatedError();
  // review_defer is a fix-cycle gate (per spec): only fix-cycle sessions
  // may defer findings. Tokens without a role (legacy / unit tests) are
  // allowed; review-zone / dedup / re-review tokens are rejected so a
  // reviewer can't accidentally defer the work of a fix agent.
  if (meta.role && meta.role !== "fix-cycle") {
    throw new GateError(
      makeGateError(
        "wrong_session_role",
        "review_defer requires a fix-cycle session",
        false,
        { actual_role: meta.role, required_role: "fix-cycle" },
      ),
    );
  }
  const rows = await deps.store.readFindings();
  const m = materializeFindings(rows);
  const f = m.get(req.finding_id);
  if (!f) {
    throw new GateError(
      makeGateError("finding_not_found", `unknown finding ${req.finding_id}`, false, {
        finding_id: req.finding_id,
      }),
    );
  }
  if (f.status !== "open" && f.status !== "fixing") {
    throw new GateError(
      makeGateError(
        "finding_already_resolved",
        `finding ${req.finding_id} already at status ${f.status}`,
        false,
        { finding_id: req.finding_id, current_status: f.status },
      ),
    );
  }
  await deps.store.appendFinding({
    kind: "status_update",
    id: generateRowId(),
    ts: new Date().toISOString(),
    ref: req.finding_id,
    status: "deferred",
    by_pass: meta.passId,
    by_session: meta.claimHandleId,
    reason: req.reason,
  });
  return { finding_id: req.finding_id, finding_status: "deferred" };
}
