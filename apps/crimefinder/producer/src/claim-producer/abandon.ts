import type { Logger } from "pino";
import type { SessionTokenRegistry } from "../state/session-tokens.js";

export interface AbandonRequest {
  claim_id: string;
}
export interface AbandonResponse {
  ok: boolean;
}

// Abandon: JSONL-durable rows already landed and the materialized status
// reflects them, so we don't have to roll anything back. We DO revoke the
// session-token bound to this claim — otherwise a token issued to a
// failed fan-out child would remain valid in memory until producer
// restart, letting a hijacked or stale agent keep calling typed-state.
export async function handleAbandon(
  req: AbandonRequest,
  tokens: SessionTokenRegistry,
  _logger: Logger,
): Promise<AbandonResponse> {
  tokens.revokeByClaim(req.claim_id);
  return { ok: true };
}
