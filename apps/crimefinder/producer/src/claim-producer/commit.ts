import type { Logger } from "pino";
import type { SessionTokenRegistry } from "../state/session-tokens.js";

export interface CommitRequest {
  claim_id: string;
}
export interface CommitResponse {
  ok: boolean;
}

// Commit is the success terminal for a claim. The durable state is already
// written to JSONL during the typed-state gate calls, so there's no rollback
// or write-out to do. But the session-token issued for this claim must be
// revoked here — otherwise it would stay live until producer restart or TTL
// expiry, letting any holder keep calling typed-state after the claim is
// done. Matches the Abandon and Release behavior.
export async function handleCommit(
  req: CommitRequest,
  tokens: SessionTokenRegistry,
  _logger: Logger,
): Promise<CommitResponse> {
  tokens.revokeByClaim(req.claim_id);
  return { ok: true };
}
