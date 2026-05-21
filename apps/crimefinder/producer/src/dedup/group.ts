/**
 * @source: prototype-repo:src/features/dedup/group.ts
 * @diverged: true
 * @reason: input type changed from prototype's Issue (multi-snippet) to
 *          FindingRow (single file). Group key is the finding's `file`
 *          rather than each snippet file.
 */

import type { FindingRow } from "@crimefinder/shared";

export function groupFindingsByFile(findings: FindingRow[]): Map<string, string[]> {
  const fileToIds = new Map<string, Set<string>>();
  for (const f of findings) {
    let ids = fileToIds.get(f.file);
    if (!ids) {
      ids = new Set();
      fileToIds.set(f.file, ids);
    }
    ids.add(f.id);
  }
  // Filter to files with 2+ findings; singletons can't have duplicates.
  for (const [file, ids] of fileToIds) {
    if (ids.size < 2) fileToIds.delete(file);
  }
  const out = new Map<string, string[]>();
  for (const [file, ids] of fileToIds) {
    out.set(file, [...ids]);
  }
  return out;
}

export interface FileGroup {
  file: string;
  findingIds: string[];
}

export interface DedupBatch {
  batchNumber: number;
  fileGroups: FileGroup[];
  findingCount: number;
}

export function batchFileGroups(
  groups: Map<string, string[]>,
  _findings: FindingRow[],
  maxPerBatch = 50,
): DedupBatch[] {
  const batches: DedupBatch[] = [];
  const sortedFiles = [...groups.entries()].sort((a, b) => b[1].length - a[1].length);
  let cur: DedupBatch = { batchNumber: 1, fileGroups: [], findingCount: 0 };
  const curIds = new Set<string>();
  for (const [file, ids] of sortedFiles) {
    let newCount = 0;
    for (const id of ids) if (!curIds.has(id)) newCount += 1;
    if (cur.fileGroups.length > 0 && curIds.size + newCount > maxPerBatch) {
      batches.push(cur);
      cur = { batchNumber: batches.length + 1, fileGroups: [], findingCount: 0 };
      curIds.clear();
    }
    cur.fileGroups.push({ file, findingIds: [...ids] });
    for (const id of ids) curIds.add(id);
    cur.findingCount = curIds.size;
  }
  if (cur.fileGroups.length > 0) batches.push(cur);
  return batches;
}
