// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

export type PriorDispatchDisposition =
  | "stale_recovery"
  | "retry_after_error"
  | "recalculate";

export interface DispatchContextSnapshot {
  dispatch_id: string;
  run_scope_id: string;
  prior_dispatch_id: string | null;
  prior_dispatch_disposition: PriorDispatchDisposition | null;
}

export type DispatchContextWarn = (event: {
  kind: "wire_contract_violation";
  message: string;
  prior_dispatch_id: string;
  prior_dispatch_disposition_wire: string;
}) => void;

export function dispatchContextSnapshot(
  dispatchId: string,
  runScopeId: string,
  priorDispatchId: string,
  priorDispatchDispositionWire: string,
  warn?: DispatchContextWarn,
): DispatchContextSnapshot {
  const disposition = mapDispositionFromWire(priorDispatchDispositionWire);
  const priorId = priorDispatchId.length > 0 ? priorDispatchId : null;
  if (priorId !== null && disposition === null && warn !== undefined) {
    warn({
      kind: "wire_contract_violation",
      message:
        "prior_dispatch_id present but prior_dispatch_disposition is " +
        "PRIOR_NONE / empty / unknown; the supervisor must send a typed " +
        "disposition whenever a prior identifier is set",
      prior_dispatch_id: priorDispatchId,
      prior_dispatch_disposition_wire: priorDispatchDispositionWire,
    });
  }
  return {
    dispatch_id: dispatchId,
    run_scope_id: runScopeId,
    prior_dispatch_id: priorId,
    prior_dispatch_disposition: priorId === null ? null : disposition,
  };
}

function mapDispositionFromWire(
  wire: string,
): PriorDispatchDisposition | null {
  switch (wire) {
    case "PRIOR_STALE_RECOVERY":
      return "stale_recovery";
    case "PRIOR_RETRY_AFTER_ERROR":
      return "retry_after_error";
    case "PRIOR_RECALCULATE":
      return "recalculate";
    case "PRIOR_NONE":
    case "":
    default:
      return null;
  }
}

export interface TokenEntry {
  runId: string;
  attributesAtSpawn: Record<string, unknown>;
  dispatchContext: DispatchContextSnapshot;
  cancelToken: string;
  nodeId: string;
  callbackUrl: string;
  onComplete: (
    attributesDelta: Record<string, unknown> | null,
    changed: boolean,
    changeSummary: string | null,
    signoffs: string[] | null,
    scheduleTeardown: (td: () => Promise<void>) => void,
  ) => Promise<
    | { status: "accepted" }
    | { status: "rejected"; errors: Record<string, string[]> }
  >;
  onBlocked: (
    reason: string,
    context: unknown,
    scheduleTeardown: (td: () => Promise<void>) => void,
  ) => Promise<void>;
  onError: (
    errorClass: string,
    payload: unknown,
    scheduleTeardown: (td: () => Promise<void>) => void,
  ) => Promise<void>;
  onPark?: (
    reason: string,
    reasonNote: string | null,
    resumeAt: string | null,
    scheduleTeardown: (td: () => Promise<void>) => void,
  ) => Promise<void>;
}

export class TokenRegistry {
  private readonly map = new Map<string, TokenEntry>();

  register(token: string, entry: TokenEntry): void {
    this.map.set(token, entry);
  }

  lookup(token: string): TokenEntry | undefined {
    return this.map.get(token);
  }

  release(token: string): void {
    this.map.delete(token);
  }

  size(): number {
    return this.map.size;
  }
}
