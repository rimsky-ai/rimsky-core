import type { FindingRow, FindingsRow, StatusUpdateRow } from "@crimefinder/shared";

export interface MaterializedFinding {
  row: FindingRow;
  status: string;
  lastUpdate?: StatusUpdateRow;
}

// Status materialization: scan all rows where id == F1 OR ref == F1; sort by
// ts; take the last status field set. Last-write-wins by timestamp.
export function materializeFindings(rows: FindingsRow[]): Map<string, MaterializedFinding> {
  const findings = new Map<string, FindingRow>();
  for (const r of rows) {
    if (r.kind === "finding") findings.set(r.id, r);
  }
  // Bucket status updates by ref, sorted by ts ascending.
  const updates = new Map<string, StatusUpdateRow[]>();
  for (const r of rows) {
    if (r.kind !== "status_update") continue;
    let bucket = updates.get(r.ref);
    if (!bucket) {
      bucket = [];
      updates.set(r.ref, bucket);
    }
    bucket.push(r);
  }
  for (const bucket of updates.values()) bucket.sort((a, b) => a.ts.localeCompare(b.ts));

  const out = new Map<string, MaterializedFinding>();
  for (const [id, row] of findings) {
    const bucket = updates.get(id) ?? [];
    const last = bucket[bucket.length - 1];
    out.set(id, { row, status: last ? last.status : row.status, lastUpdate: last });
  }
  return out;
}
