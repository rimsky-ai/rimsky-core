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
   * writeback POSTs to `{callback_url}/v1/attributes/{node_id}` (per spec
   * §12.5).
   */
  cancelToken: string;
  /**
   * Supervisor-side `node_id` — used as the path segment on the writeback URL.
   */
  nodeId: string;
  /**
   * Supervisor-issued callback base URL. The writeback URL is
   * `${callbackUrl}/v1/attributes/${nodeId}`.
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
