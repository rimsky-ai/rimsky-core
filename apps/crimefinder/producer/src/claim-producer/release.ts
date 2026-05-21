import type { Logger } from "pino";
import type { SessionTokenRegistry } from "../state/session-tokens.js";

export interface ReleaseRequest {
  claim_id: string;
}
export interface ReleaseResponse {
  ok: boolean;
}

export async function handleRelease(
  req: ReleaseRequest,
  tokens: SessionTokenRegistry,
  _logger: Logger,
): Promise<ReleaseResponse> {
  tokens.revokeByClaim(req.claim_id);
  return { ok: true };
}
