import type { Logger } from "pino";
import { NamedEventEnvelope, makeNamedEvent, NamedEventName } from "@crimefinder/shared";
import type { StateClient } from "../state-client.js";

export interface NamedEventEmitter {
  emit(env: NamedEventEnvelope): void;
}

export interface GateContext {
  stateClient: StateClient;
  emit: NamedEventEmitter;
  passId: string;
  zoneId?: string;
  sessionId: string;
  role: "review-zone" | "fix-cycle" | "dedup" | "re-review";
  logger: Logger;
  // Optional ContextPayload data the executor cached at dispatch time
  // (zone_label, zone_files, repoRoot...). Gates that need it (review_context
  // for the review-zone role) read it here.
  zoneLabel?: string;
  zoneFiles?: string[];
  mission?: string;
  // For fix-cycle dispatches: the producer hands back a list of finding
  // IDs the agent is responsible for. Passed through to GetReviewContext
  // so the producer can scope the payload to just those rows.
  assignedFindingIds?: string[];
  // For fix-cycle dispatches: the iter-N this fix belongs to. Threaded
  // into finding_resolved named-events so observability consumers can
  // bucket resolutions by iteration.
  iterNum?: number;
}

export function event(
  ctx: GateContext,
  name: NamedEventName,
  data: Record<string, unknown>,
): NamedEventEnvelope {
  return makeNamedEvent(name, {
    passId: ctx.passId,
    zoneId: ctx.zoneId,
    sessionId: ctx.sessionId,
    data,
  });
}
