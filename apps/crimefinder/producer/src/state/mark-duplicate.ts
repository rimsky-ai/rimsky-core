import { generateRowId, GateError, makeGateError } from "@crimefinder/shared";
import { StateHandlerDeps, UnauthenticatedError } from "./handler-deps.js";
import { applyDedupResults } from "../dedup/resolve.js";

export interface MarkDuplicateRequest {
  session_token: string;
  finding_id: string;
  duplicate_of: string;
}

export interface MarkDuplicateResponse {
  success: boolean;
  // True iff the cross-batch conservative-conflict rule (`finding_id`
  // appears as both a survivor and a duplicate elsewhere) suppressed
  // the marking and kept the row open.
  skipped_due_to_conflict?: boolean;
}

// Marks `finding_id` as a duplicate of `duplicate_of`. Used by the dedup
// agent through the review_dedup_mark gate. Only valid from a dedup-role
// session.
export async function handleMarkDuplicate(
  req: MarkDuplicateRequest,
  deps: StateHandlerDeps,
): Promise<MarkDuplicateResponse> {
  const meta = deps.tokens.validate(req.session_token);
  if (!meta) throw new UnauthenticatedError();
  if (meta.role !== "dedup") {
    throw new GateError(
      makeGateError(
        "wrong_session_role",
        "review_dedup_mark requires a dedup session",
        false,
        { actual_role: meta.role ?? null, required_role: "dedup" },
      ),
    );
  }
  // These are caller-input validation failures, not lookup misses. Using
  // `finding_not_found` here would mislead the agent into thinking one of
  // the ids was simply absent. Distinct `invalid_request` makes the failure
  // mode unambiguous.
  if (!req.finding_id || !req.duplicate_of) {
    throw new GateError(
      makeGateError(
        "invalid_request",
        "review_dedup_mark requires both finding_id and duplicate_of",
        false,
        {
          missing_field: !req.finding_id ? "finding_id" : "duplicate_of",
        },
      ),
    );
  }
  if (req.finding_id === req.duplicate_of) {
    throw new GateError(
      makeGateError(
        "invalid_request",
        "finding_id and duplicate_of must differ",
        false,
        { finding_id: req.finding_id },
      ),
    );
  }
  // Conservative cross-batch conflict resolution: feed the current
  // proposal plus prior dedup decisions into applyDedupResults and
  // commit only the intents that survive. If `finding_id` ALSO appears
  // as a `duplicate_of` value in some other row (i.e. another batch
  // treated it as a survivor), applyDedupResults filters it out and
  // the row stays open.
  const allRows = await deps.store.readFindings();
  const priorIntents: Array<{ findingId: string; duplicateOf: string }> = [];
  for (const r of allRows) {
    if (r.kind === "status_update" && r.status === "duplicate-of" && r.duplicate_of) {
      priorIntents.push({ findingId: r.ref, duplicateOf: r.duplicate_of });
    }
  }
  const synthetic = applyDedupResults([
    {
      duplicateGroups: [
        {
          survivorId: req.duplicate_of,
          duplicateIds: [req.finding_id],
        },
      ],
    },
    {
      duplicateGroups: priorIntents.reduce<Array<{ survivorId: string; duplicateIds: string[] }>>(
        (acc, p) => {
          const existing = acc.find((g) => g.survivorId === p.duplicateOf);
          if (existing) existing.duplicateIds.push(p.findingId);
          else acc.push({ survivorId: p.duplicateOf, duplicateIds: [p.findingId] });
          return acc;
        },
        [],
      ),
    },
  ]);
  const wantedIntent = synthetic.find(
    (i) => i.findingId === req.finding_id && i.duplicateOf === req.duplicate_of,
  );
  if (!wantedIntent) {
    return { success: true, skipped_due_to_conflict: true };
  }
  await deps.store.appendFinding({
    kind: "status_update",
    id: generateRowId(),
    ts: new Date().toISOString(),
    ref: req.finding_id,
    status: "duplicate-of",
    by_pass: meta.passId,
    by_session: meta.claimHandleId,
    duplicate_of: req.duplicate_of,
  });
  return { success: true };
}
