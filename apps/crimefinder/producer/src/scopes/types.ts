import type { Logger } from "pino";
import type { JsonlStore } from "../jsonl-store.js";
import type { SessionTokenRegistry } from "../state/session-tokens.js";
import type { IterationCounter } from "../state/iteration-counter.js";
import type { CrimefinderConfig } from "../config.js";
import type { Zone } from "../zones/partition.js";
import type { FileGroup } from "../dedup/group.js";
import type { GitOps } from "../git-ops.js";

export interface OpenContext {
  selector: string;
  claimId: string;
  repoRoot: string;
  store: JsonlStore;
  tokens: SessionTokenRegistry;
  iterCounter: IterationCounter;
  stateEndpointUrl: string;
  partitionCache: PartitionCache;
  config: CrimefinderConfig;
  git: GitOps;
  logger: Logger;
}

export interface OpenResult {
  address: Uint8Array;
  payload: Uint8Array;
  scope: Uint8Array;
}

// Per-pass cache of partitioning plans + ad-hoc state shared across scope
// handlers within one pass. Lives in-process; lost on producer restart, in
// which case downstream scopes recompute against the JSONL substrate.
export interface PartitionCache {
  setZonePlan(passId: string, zones: Zone[]): void;
  getZonePlan(passId: string): Zone[] | undefined;
  setDedupBatches(passId: string, batches: FileGroup[][]): void;
  getDedupBatches(passId: string): FileGroup[][] | undefined;
  setAffectedZones(passId: string, iterNum: number, zones: Zone[]): void;
  getAffectedZones(passId: string, iterNum: number): Zone[] | undefined;
}

export function createPartitionCache(): PartitionCache {
  const zonePlans = new Map<string, Zone[]>();
  const dedupBatches = new Map<string, FileGroup[][]>();
  const affected = new Map<string, Zone[]>();
  const key = (p: string, i: number) => `${p}#${i}`;
  return {
    setZonePlan(p, z) {
      zonePlans.set(p, z);
    },
    getZonePlan(p) {
      return zonePlans.get(p);
    },
    setDedupBatches(p, b) {
      dedupBatches.set(p, b);
    },
    getDedupBatches(p) {
      return dedupBatches.get(p);
    },
    setAffectedZones(p, i, z) {
      affected.set(key(p, i), z);
    },
    getAffectedZones(p, i) {
      return affected.get(key(p, i));
    },
  };
}

export function parseSelectorQuery(selector: string): Record<string, string> {
  // Selectors are of the form `@kind:k1=v1&k2=v2&...`.
  const idx = selector.indexOf(":");
  const query = idx >= 0 ? selector.slice(idx + 1) : "";
  const out: Record<string, string> = {};
  if (!query) return out;
  for (const pair of query.split("&")) {
    const eq = pair.indexOf("=");
    if (eq < 0) {
      out[decodeURIComponent(pair)] = "";
    } else {
      out[decodeURIComponent(pair.slice(0, eq))] = decodeURIComponent(pair.slice(eq + 1));
    }
  }
  return out;
}
