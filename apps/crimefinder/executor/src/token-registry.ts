import crypto from "node:crypto";

// Per-run bearer token, validated on every MCP tool call. Issued at
// agent spawn; revoked when the run terminates. No persistence.

export interface TokenRecord {
  runId: string;
  issuedAt: number;
}

export class McpTokenRegistry {
  private readonly tokens = new Map<string, TokenRecord>();

  issue(runId: string): string {
    const tok = crypto.randomBytes(24).toString("base64url");
    this.tokens.set(tok, { runId, issuedAt: Date.now() });
    return tok;
  }

  validate(tok: string): TokenRecord | null {
    return this.tokens.get(tok) ?? null;
  }

  revoke(tok: string): void {
    this.tokens.delete(tok);
  }
}
