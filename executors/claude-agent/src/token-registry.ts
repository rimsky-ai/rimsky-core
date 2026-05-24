// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

/**
 * Per-executor, in-memory token → callback map.
 *
 * @source rimsky/src/callback-mcp/token-registry.ts
 *
 * Each agent run registers a short-lived token that the spawned Claude CLI
 * subprocess presents on every internal-MCP tool call. Tokens are released
 * when the run finishes, is torn down, or the executor restarts. No
 * persistence — tokens do not survive restart.
 *
 * Spec: docs/specs/2026-04-25-stores-redesign-design.md §12. The legacy
 * `result` field on `onComplete` was retired in the §12 protocol rewrite;
 * `onComplete` now optionally carries an `attributesDelta` for the
 * terminal-final writeback pattern, and is empty for the
 * incremental-via-callback pattern.
 */
export interface TokenEntry {
  runId: string;
  /**
   * The dispatch-time attributes object, as `claude-agent` captured it from
   * `ExecuteRequest.attributes`. Returned verbatim by the `attributes_read`
   * MCP tool. Read-only snapshot; does not refresh.
   */
  attributesAtSpawn: Record<string, unknown>;
  /**
   * Supervisor-issued cancel-token; carried as bearer auth on incremental
   * writeback POSTs to `{callback_url}/v1/runs/{run_id}/attributes` (per
   * spec §12.5; URL shape changed in the 2026-05-20 per-run keying refactor).
   */
  cancelToken: string;
  /**
   * Supervisor-side `node_id` — denormalized for forensic queries; the path
   * segment on the writeback URL is `run_id` (= dispatch_id).
   */
  nodeId: string;
  /**
   * Supervisor-issued callback base URL. The writeback URL is
   * `${callbackUrl}/v1/runs/${runId}/attributes`.
   */
  callbackUrl: string;
  onComplete: (
    attributesDelta: Record<string, unknown> | null,
    changed: boolean,
    changeSummary: string | null,
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
  /**
   * Park the dispatch. `reason` is the typed ParkReason snake_case
   * value from the closed two-value set (await_callback | snooze) per
   * spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.
   * `resumeAt` is optional ISO 8601.
   */
  onPark?: (
    reason: string,
    reasonNote: string | null,
    resumeAt: string | null,
    scheduleTeardown: (td: () => Promise<void>) => void,
  ) => Promise<void>;
  /**
   * Persist a partial attribute write via the supervisor's incremental
   * writeback callback. Returns the HTTP status from the supervisor so the
   * tool dispatcher can surface a structured result to the agent.
   */
  onAttributesSet: (
    delta: Record<string, unknown>,
  ) => Promise<{ status: number }>;
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
