import { generateRowId } from "@crimefinder/shared";
import { StateHandlerDeps, UnauthenticatedError } from "./handler-deps.js";

export interface RequestHelpRequest {
  session_token: string;
  question: string;
  blocker_finding_id?: string;
}

export async function handleRequestHelp(
  req: RequestHelpRequest,
  deps: StateHandlerDeps,
): Promise<{ help_id: string }> {
  const meta = deps.tokens.validate(req.session_token);
  if (!meta) throw new UnauthenticatedError();
  const id = generateRowId();
  await deps.store.appendFinding({
    kind: "help_request",
    id,
    ts: new Date().toISOString(),
    pass_id: meta.passId,
    session_id: meta.claimHandleId,
    question: req.question,
    blocker_finding_id: req.blocker_finding_id || null,
    status: "open",
  });
  return { help_id: id };
}
