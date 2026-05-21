import fs from "node:fs/promises";
import path from "node:path";
import { generateSessionToken } from "@crimefinder/shared";

export type SessionRole = "review-zone" | "fix-cycle" | "dedup" | "re-review";

export interface TokenMetadata {
  passId: string;
  claimHandleId: string;
  zoneId?: string;
  // For dedup sessions: which batch_index this session owns. Lets
  // handleGetReviewContext look up the batch's file_groups in the
  // partition cache when delivering the role:"dedup" payload.
  batchIndex?: number;
  role?: SessionRole;
  issuedAt: number;
}

// Persisted-row shape. Each newly-issued token appends a row; each revoke
// appends a tombstone. The replayer reconstructs the live map by applying
// rows in order and dropping any token whose tombstone is present or whose
// age > TTL_MS.
interface PersistedRow {
  kind: "issue" | "revoke";
  ts: number; // ms epoch
  token: string;
  meta?: TokenMetadata;
}

const DEFAULT_TTL_MS = 24 * 60 * 60 * 1000;
const TOKENS_FILE = "tokens.jsonl";

export interface SessionTokenRegistryOptions {
  // When set, every issue/revoke is appended to `<storeDir>/tokens.jsonl`
  // and a process-restart can rehydrate via reload(). When unset, the
  // registry is in-memory only (used in unit tests).
  storeDir?: string;
  ttlMs?: number;
  now?: () => number;
}

// SessionTokenRegistry holds the {token → metadata} map gates use to
// authenticate typed-state calls. With `storeDir` set, the registry is
// also durable across producer restarts via JSONL append + reload, so a
// crash mid-pass doesn't invalidate every in-flight session-token.
export class SessionTokenRegistry {
  private readonly tokens = new Map<string, TokenMetadata>();
  // Inverse index claimHandleId → token, so Release can revoke without
  // requiring callers to remember the token.
  private readonly byClaim = new Map<string, string>();
  // Per-token registry-side issue timestamp, used for TTL enforcement.
  // The caller-supplied `meta.issuedAt` is kept as informational metadata
  // for the gate; TTL uses the registry's own clock so tests passing
  // `issuedAt: 0` don't trip the validate-time TTL check.
  private readonly registryIssuedAt = new Map<string, number>();
  private readonly storeDir?: string;
  private readonly ttlMs: number;
  private readonly now: () => number;
  // Single-writer chain so concurrent issue/revoke calls serialize their
  // appends (the registry is process-local but called from many request
  // handlers).
  private writeQueue: Promise<void> = Promise.resolve();

  constructor(opts: SessionTokenRegistryOptions = {}) {
    this.storeDir = opts.storeDir;
    this.ttlMs = opts.ttlMs ?? DEFAULT_TTL_MS;
    this.now = opts.now ?? (() => Date.now());
  }

  private filePath(): string | null {
    return this.storeDir ? path.join(this.storeDir, TOKENS_FILE) : null;
  }

  private async append(row: PersistedRow): Promise<void> {
    const p = this.filePath();
    if (!p) return;
    const prev = this.writeQueue;
    let release!: () => void;
    const next = new Promise<void>((r) => {
      release = r;
    });
    this.writeQueue = prev.then(() => next);
    await prev;
    try {
      await fs.mkdir(this.storeDir!, { recursive: true });
      await fs.appendFile(p, JSON.stringify(row) + "\n", "utf-8");
    } finally {
      release();
    }
  }

  issue(meta: TokenMetadata): string {
    const token = generateSessionToken();
    this.tokens.set(token, meta);
    this.byClaim.set(meta.claimHandleId, token);
    const ts = this.now();
    this.registryIssuedAt.set(token, ts);
    void this.append({ kind: "issue", ts, token, meta });
    return token;
  }

  validate(token: string): TokenMetadata | null {
    const meta = this.tokens.get(token);
    if (!meta) return null;
    // Apply the same TTL check used at reload(): a long-running producer
    // holding a token past its TTL must reject it rather than continue to
    // accept stale credentials. Use the registry-side issue timestamp
    // (recorded by issue()) rather than `meta.issuedAt` so callers that
    // pass `issuedAt: 0` (e.g. unit tests) don't get spuriously rejected.
    const registryTs = this.registryIssuedAt.get(token) ?? meta.issuedAt;
    if (registryTs + this.ttlMs <= this.now()) {
      this.tokens.delete(token);
      this.byClaim.delete(meta.claimHandleId);
      this.registryIssuedAt.delete(token);
      void this.append({ kind: "revoke", ts: this.now(), token });
      return null;
    }
    return meta;
  }

  revoke(token: string): void {
    const meta = this.tokens.get(token);
    if (!meta) return;
    this.tokens.delete(token);
    this.byClaim.delete(meta.claimHandleId);
    this.registryIssuedAt.delete(token);
    void this.append({ kind: "revoke", ts: this.now(), token });
  }

  revokeByClaim(claimHandleId: string): void {
    const token = this.byClaim.get(claimHandleId);
    if (!token) return;
    this.revoke(token);
  }

  // Replay the persisted log into the in-memory map. Skips tokens whose
  // tombstone is present or whose age exceeds the TTL.
  async reload(): Promise<{ loaded: number; expired: number; revoked: number }> {
    this.tokens.clear();
    this.byClaim.clear();
    this.registryIssuedAt.clear();
    const p = this.filePath();
    if (!p) return { loaded: 0, expired: 0, revoked: 0 };
    let raw: string;
    try {
      raw = await fs.readFile(p, "utf-8");
    } catch (e) {
      if ((e as NodeJS.ErrnoException).code === "ENOENT") {
        return { loaded: 0, expired: 0, revoked: 0 };
      }
      throw e;
    }
    const issues = new Map<string, PersistedRow>();
    const revokes = new Set<string>();
    for (const line of raw.split("\n")) {
      const t = line.trim();
      if (!t) continue;
      let row: PersistedRow;
      try {
        row = JSON.parse(t) as PersistedRow;
      } catch {
        continue;
      }
      if (row.kind === "issue" && row.meta) issues.set(row.token, row);
      if (row.kind === "revoke") revokes.add(row.token);
    }
    let loaded = 0;
    let expired = 0;
    let revoked = 0;
    const cutoff = this.now() - this.ttlMs;
    for (const [token, row] of issues) {
      if (revokes.has(token)) {
        revoked += 1;
        continue;
      }
      if (row.ts < cutoff) {
        expired += 1;
        continue;
      }
      if (row.meta) {
        this.tokens.set(token, row.meta);
        this.byClaim.set(row.meta.claimHandleId, token);
        this.registryIssuedAt.set(token, row.ts);
        loaded += 1;
      }
    }
    return { loaded, expired, revoked };
  }
}
