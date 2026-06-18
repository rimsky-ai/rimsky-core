// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

export interface TokenEntry {
  runId: string;
  attributesAtSpawn: Record<string, unknown>;
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
