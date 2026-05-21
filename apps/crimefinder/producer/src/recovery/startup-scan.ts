import type { Logger } from "pino";
import { generateRowId } from "@crimefinder/shared";
import type { JsonlStore } from "../jsonl-store.js";
import type { GitOps } from "../git-ops.js";
import type { IterationCounter } from "../state/iteration-counter.js";
import type { PartitionCache } from "../scopes/types.js";

const RESOLVES_RE = /^Resolves:\s+(f_[a-z2-7]+)\s*$/m;

export interface StartupRecoveryDeps {
  store: JsonlStore;
  git: GitOps;
  iterCounter: IterationCounter;
  // Optional: when supplied, restore persisted zone plans into the in-memory
  // partition cache so post-restart Open/SplitScope calls see the same
  // partition the original pass computed. Without this, a producer restart
  // mid-pass would silently lose every zone and finding zone_ids would
  // start drifting toward `z_unknown`.
  partitionCache?: PartitionCache;
  repoRoot: string;
  logger: Logger;
}

export interface StartupRecoveryResult {
  reconstructedRowsAppended: number;
  iterationCountersRebuilt: number;
  zonePlansRestored: number;
  dedupBatchesRestored: number;
}

export async function runStartupRecovery(deps: StartupRecoveryDeps): Promise<StartupRecoveryResult> {
  const findings = await deps.store.readFindings();
  // Collect every (finding_id, commit_sha) already recorded as fixed.
  const knownResolved = new Set<string>();
  const findingByPass = new Map<string, string>();
  // Track the most-recent commit SHA we already have a status_update
  // for; we ask git.log for everything since that SHA rather than the
  // default last-500. The "most recent" pick uses timestamp order so a
  // resync on a long-lived repo doesn't drift.
  let mostRecentKnownSha: string | null = null;
  let mostRecentKnownTs: string | null = null;
  for (const r of findings) {
    if (r.kind === "finding") findingByPass.set(r.id, r.pass_id);
    if (r.kind === "status_update" && r.status === "fixed" && r.resolved_at_commit) {
      knownResolved.add(`${r.ref}@${r.resolved_at_commit}`);
      if (!mostRecentKnownTs || r.ts > mostRecentKnownTs) {
        mostRecentKnownTs = r.ts;
        mostRecentKnownSha = r.resolved_at_commit;
      }
    }
  }

  let commits: Awaited<ReturnType<GitOps["log"]>>;
  try {
    commits = await deps.git.log(deps.repoRoot, mostRecentKnownSha ?? undefined);
  } catch (e) {
    deps.logger.warn({ err: String(e) }, "recovery_log_failed");
    commits = [];
  }

  let appended = 0;
  for (const commit of commits) {
    const match = RESOLVES_RE.exec(commit.body);
    if (!match) continue;
    const findingId = match[1];
    if (knownResolved.has(`${findingId}@${commit.sha}`)) continue;
    const passId = findingByPass.get(findingId) ?? "unknown";
    let ts: string;
    try {
      ts = await deps.git.showCommitTimestamp(deps.repoRoot, commit.sha);
    } catch {
      ts = new Date().toISOString();
    }
    await deps.store.appendFinding({
      kind: "status_update",
      id: generateRowId(),
      ts,
      ref: findingId,
      status: "fixed",
      by_pass: passId,
      by_session: "recovery-scan",
      resolved_at_commit: commit.sha,
      note: "reconstructed by startup recovery",
    });
    appended += 1;
    deps.logger.info(
      { findingId, sha: commit.sha },
      "recovery_reconstructed_status_fixed",
    );
  }

  await deps.iterCounter.restore();

  // Restore persisted zone plans + dedup batches into the partition cache.
  // Multiple rows of either kind for the same pass_id may exist if the
  // pass crashed and resumed; the last-wins ordering matches the Open
  // paths in openSourceTree / openDedupGrouping, which only write when
  // the cache is empty.
  //
  // Ordering uses the monotonic `seq` field assigned by the producer
  // under the passes-file mutex (see jsonl-store.appendZonePlan /
  // appendDedupBatches). The previous code ordered by ISO-8601 `ts`,
  // which tied under high throughput (millisecond granularity) and
  // resolved in map-iteration order — an older plan could win and
  // detach downstream findings from current zone IDs. Falls back to
  // `ts` for legacy rows without `seq`, and finally to the row's
  // position in the passes file (later append wins) so two legacy
  // rows that share an identical `ts` still resolve deterministically
  // to the most-recently-written row.
  let zonePlansRestored = 0;
  let dedupBatchesRestored = 0;
  if (deps.partitionCache) {
    const passes = await deps.store.readPasses();
    type Ord = { seq: number; ts: string; position: number; row: typeof passes[number] };
    const cmp = (a: Ord, b: Ord): number => {
      // Higher seq wins. -1 (= no seq) is treated as the smallest, so a
      // legacy row only wins if no seq'd row exists for that pass.
      if (a.seq !== b.seq) return a.seq < b.seq ? -1 : 1;
      if (a.ts !== b.ts) return a.ts < b.ts ? -1 : 1;
      // Final tiebreaker: row position in the appended file. The later
      // append wins. Without this, two legacy rows (no seq) sharing an
      // identical ms-granularity ts would tie and the first-inserted
      // map entry would win by accident.
      if (a.position !== b.position) return a.position < b.position ? -1 : 1;
      return 0;
    };
    const lastZoneByPass = new Map<string, Ord>();
    const lastDedupByPass = new Map<string, Ord>();
    for (let i = 0; i < passes.length; i++) {
      const p = passes[i];
      if (p.kind === "zone_plan") {
        const cur: Ord = {
          seq: typeof p.seq === "number" ? p.seq : -1,
          ts: p.ts,
          position: i,
          row: p,
        };
        const prev = lastZoneByPass.get(p.pass_id);
        if (!prev || cmp(cur, prev) > 0) lastZoneByPass.set(p.pass_id, cur);
      } else if (p.kind === "dedup_batches") {
        const cur: Ord = {
          seq: typeof p.seq === "number" ? p.seq : -1,
          ts: p.ts,
          position: i,
          row: p,
        };
        const prev = lastDedupByPass.get(p.pass_id);
        if (!prev || cmp(cur, prev) > 0) lastDedupByPass.set(p.pass_id, cur);
      }
    }
    for (const { row } of lastZoneByPass.values()) {
      if (row.kind !== "zone_plan") continue;
      deps.partitionCache.setZonePlan(row.pass_id, row.zones);
      zonePlansRestored += 1;
    }
    for (const { row } of lastDedupByPass.values()) {
      if (row.kind !== "dedup_batches") continue;
      // Persisted batches use snake-cased `finding_ids` for wire/disk
      // consistency; the in-memory FileGroup shape uses camelCase
      // `findingIds`. Translate on rehydrate so downstream consumers see
      // the same shape `openDedupGrouping` would have produced live.
      const batches = row.batches.map((b) =>
        b.map((g) => ({ file: g.file, findingIds: g.finding_ids })),
      );
      deps.partitionCache.setDedupBatches(row.pass_id, batches);
      dedupBatchesRestored += 1;
    }
  }

  return {
    reconstructedRowsAppended: appended,
    iterationCountersRebuilt: 1,
    zonePlansRestored,
    dedupBatchesRestored,
  };
}
