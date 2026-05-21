import { generateRowId, GateError, makeGateError } from "@crimefinder/shared";
import { StateHandlerDeps, UnauthenticatedError } from "./handler-deps.js";

const ALLOWED_STATUSES = new Set([
  "fixing",
  "fixed",
  "deferred",
  "duplicate-of",
  "void",
  "queued-to-spec",
  "resolved-via-spec",
]);

export interface UpdateFindingStatusRequest {
  session_token: string;
  finding_id: string;
  status: string;
  reason?: string;
  note?: string;
  duplicate_of?: string;
}

export async function handleUpdateFindingStatus(
  req: UpdateFindingStatusRequest,
  deps: StateHandlerDeps,
): Promise<{ success: boolean }> {
  const meta = deps.tokens.validate(req.session_token);
  if (!meta) throw new UnauthenticatedError();
  // Plain Errors fall through wrapHandler to INTERNAL (untyped, retryable
  // semantics). Validation failures here are caller-fixable (invalid_status
  // is a typo, missing reason is a malformed request), so they need to be
  // GateError so wrapHandler maps to FAILED_PRECONDITION with an error_class.
  if (!ALLOWED_STATUSES.has(req.status)) {
    throw new GateError(
      makeGateError("invalid_status", `invalid status: ${req.status}`, false, {
        status: req.status,
        allowed: [...ALLOWED_STATUSES],
      }),
    );
  }
  if ((req.status === "deferred" || req.status === "void") && !req.reason) {
    throw new GateError(
      makeGateError(
        "invalid_request",
        `status ${req.status} requires a reason`,
        false,
        { status: req.status, missing_field: "reason" },
      ),
    );
  }
  if (req.status === "duplicate-of" && !req.duplicate_of) {
    throw new GateError(
      makeGateError(
        "invalid_request",
        "status duplicate-of requires duplicate_of",
        false,
        { status: req.status, missing_field: "duplicate_of" },
      ),
    );
  }
  await deps.store.appendFinding({
    kind: "status_update",
    id: generateRowId(),
    ts: new Date().toISOString(),
    ref: req.finding_id,
    status: req.status as "fixed",
    by_pass: meta.passId,
    by_session: meta.claimHandleId,
    reason: req.reason,
    note: req.note,
    duplicate_of: req.duplicate_of,
  });
  return { success: true };
}
