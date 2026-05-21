/**
 * @source: prototype-repo:src/features/dedup/resolve.ts
 * @diverged: true
 * @reason: removed direct IssueStore coupling; returns a list of
 *          status-update intents so the caller (a typed-state handler)
 *          owns the actual JSONL append. Wired into review_dedup_mark
 *          for cross-batch conflict resolution.
 */

export interface DedupGroup {
  survivorId: string;
  duplicateIds: string[];
}
export interface DedupResult {
  duplicateGroups: DedupGroup[];
}
export interface StatusUpdateIntent {
  findingId: string;
  duplicateOf: string;
}

export function applyDedupResults(allResults: DedupResult[]): StatusUpdateIntent[] {
  const survivorIds = new Set<string>();
  for (const r of allResults) {
    for (const g of r.duplicateGroups) survivorIds.add(g.survivorId);
  }
  const map = new Map<string, string>();
  for (const r of allResults) {
    for (const g of r.duplicateGroups) {
      for (const dup of g.duplicateIds) {
        if (survivorIds.has(dup)) continue;
        if (!map.has(dup)) map.set(dup, g.survivorId);
      }
    }
  }
  return [...map.entries()].map(([findingId, duplicateOf]) => ({ findingId, duplicateOf }));
}
