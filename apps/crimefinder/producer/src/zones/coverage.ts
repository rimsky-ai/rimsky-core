/**
 * @source: prototype-repo:src/features/zones/coverage.ts
 * @diverged: true
 * @reason: storage swapped from SQLite to .crimefinder/coverage.jsonl
 *          (computeZoneCoverage takes coverageRows directly now;
 *          partitioning math is preserved).
 */

import type { CoverageRow } from "@crimefinder/shared";
import type { Zone } from "./partition.js";

export interface ZoneCoverageSummary {
  zoneId: string;
  zoneLabel: string;
  totalFiles: number;
  filesChecked: number;
  coveragePercent: number;
  sessionsSpent: number;
}

export function computeZoneCoverage(
  coverageRows: CoverageRow[],
  passId: string,
  zones: Zone[],
): ZoneCoverageSummary[] {
  const byZone = new Map<string, { files: Set<string>; sessions: Set<string> }>();
  for (const r of coverageRows) {
    if (r.pass_id !== passId) continue;
    let agg = byZone.get(r.zone_id);
    if (!agg) {
      agg = { files: new Set(), sessions: new Set() };
      byZone.set(r.zone_id, agg);
    }
    agg.files.add(r.file);
    agg.sessions.add(r.session_id);
  }

  return zones.map((zone) => {
    const agg = byZone.get(zone.id);
    const zoneFiles = new Set(zone.files);
    let filesChecked = 0;
    if (agg) {
      for (const f of agg.files) {
        if (zoneFiles.has(f)) filesChecked += 1;
      }
    }
    const sessionsSpent = agg?.sessions.size ?? 0;
    const pct = zone.files.length > 0 ? Math.round((filesChecked / zone.files.length) * 100) : 100;
    return {
      zoneId: zone.id,
      zoneLabel: zone.label,
      totalFiles: zone.files.length,
      filesChecked,
      coveragePercent: pct,
      sessionsSpent,
    };
  });
}

export function mapFileToZone(filePath: string, zones: Zone[]): Zone | null {
  for (const zone of zones) {
    if (zone.files.includes(filePath)) return zone;
  }
  return null;
}
