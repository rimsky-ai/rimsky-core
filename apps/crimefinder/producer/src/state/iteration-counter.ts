import type { Logger } from "pino";
import { generateRowId } from "@crimefinder/shared";
import type { JsonlStore } from "../jsonl-store.js";

// Per-pass iteration counter. Durable via iter_marker rows in passes.jsonl —
// must survive producer crash mid-pass because iter_num appears in selectors
// (e.g. @fix-partition:pass_id=...&iter_num=N).
//
// nextFor() is idempotent against claim_id: if rimsky retries an Open with
// the same claim_id (e.g. after a transient network failure), we return the
// existing iter_num rather than advance the counter. Concurrent calls for
// the same passId serialize through a per-pass lock so two simultaneous
// callers can't race-double-bump.

export class IterationCounter {
  private readonly byPass = new Map<string, number>();
  // claim_id → already-issued iter_num for that claim, so a retry of the
  // same Open returns the same value. Persisted via iter_marker.claim_id
  // (see schema in shared/jsonl-rows.ts).
  private readonly byClaim = new Map<string, number>();
  // Per-pass single-writer queue for nextFor; protects byPass/byClaim and
  // the row-append from interleaving.
  private readonly perPassQueue = new Map<string, Promise<void>>();
  private readonly store: JsonlStore;
  private readonly logger: Logger;

  constructor(store: JsonlStore, logger: Logger) {
    this.store = store;
    this.logger = logger;
  }

  async restore(): Promise<void> {
    const passes = await this.store.readPasses();
    for (const row of passes) {
      if (row.kind !== "iter_marker") continue;
      const prev = this.byPass.get(row.pass_id) ?? 0;
      if (row.iter_num > prev) this.byPass.set(row.pass_id, row.iter_num);
      if (row.claim_id) this.byClaim.set(row.claim_id, row.iter_num);
    }
  }

  private async runUnderPassLock<T>(passId: string, fn: () => Promise<T>): Promise<T> {
    const prev = this.perPassQueue.get(passId) ?? Promise.resolve();
    let release!: () => void;
    const next = new Promise<void>((r) => {
      release = r;
    });
    // Store the queued tail Promise in a local so the cleanup check can
    // do a reference-equality compare against the *same* Promise object
    // we just inserted — recomputing `prev.then(() => next)` would create
    // a fresh Promise, which is never reference-equal to the stored tail.
    const tail = prev.then(() => next);
    this.perPassQueue.set(passId, tail);
    try {
      await prev;
      return await fn();
    } finally {
      release();
      if (this.perPassQueue.get(passId) === tail) {
        // Best-effort cleanup; benign if not the current tail.
        this.perPassQueue.delete(passId);
      }
    }
  }

  // nextFor(passId, claimId) is idempotent on claimId: a duplicate
  // claim_id returns the previously-issued iter_num and does NOT append
  // a new iter_marker.
  async nextFor(passId: string, claimId?: string): Promise<number> {
    return this.runUnderPassLock(passId, async () => {
      if (claimId !== undefined) {
        const existing = this.byClaim.get(claimId);
        if (existing !== undefined) return existing;
      }
      const next = (this.byPass.get(passId) ?? 0) + 1;
      await this.store.appendIterMarker({
        kind: "iter_marker",
        id: generateRowId(),
        ts: new Date().toISOString(),
        pass_id: passId,
        iter_num: next,
        claim_id: claimId,
      });
      this.byPass.set(passId, next);
      if (claimId !== undefined) this.byClaim.set(claimId, next);
      this.logger.debug({ passId, iter_num: next, claimId }, "iter_advance");
      return next;
    });
  }

  currentFor(passId: string): number {
    return this.byPass.get(passId) ?? 0;
  }
}
