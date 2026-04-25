/**
 * Per-executor, in-memory token → callback map.
 *
 * @source rimsky/src/callback-mcp/token-registry.ts
 *
 * Each agent run registers a short-lived token that the spawned Claude CLI
 * subprocess presents when calling `report_complete`, `report_blocked`, or
 * `report_error`. Tokens are released when the run finishes, is torn down, or
 * the executor restarts. No persistence — tokens do not survive restart.
 */
export interface TokenEntry {
  runId: string;
  resultSchema: unknown;
  onComplete: (
    result: unknown,
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
