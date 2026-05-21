import type { ExecutorErrorClass, NamedEventEnvelope } from "@crimefinder/shared";

// Internal pre-wire outcome from runAgent. Mirrors the AsyncCallbackBody
// proto's terminal oneof; T44's server.ts maps each variant directly.
export type AgentOutcomeBase = {
  events: NamedEventEnvelope[];
};
export type AgentOutcomeSuccess = AgentOutcomeBase & {
  variant: "success";
  attributesDelta?: Record<string, unknown>;
  changed?: boolean;
  changeSummary?: string | null;
};
export type AgentOutcomeError = AgentOutcomeBase & {
  variant: "error";
  errorClass: ExecutorErrorClass;
  payload?: Uint8Array;
};
export type AgentOutcomePark = AgentOutcomeBase & {
  variant: "park";
  reason: string;
  reasonNote?: string;
  resumeAt?: string;
};
export type AgentOutcome = AgentOutcomeSuccess | AgentOutcomeError | AgentOutcomePark;
