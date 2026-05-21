import { NoOpAddressSchema, generateRowId } from "@crimefinder/shared";
import type { OpenContext, OpenResult } from "./types.js";
import { parseSelectorQuery } from "./types.js";
import { partitionIntoZones } from "../zones/partition.js";

export async function openSourceTree(ctx: OpenContext): Promise<OpenResult> {
  const q = parseSelectorQuery(ctx.selector);
  const passId = q.pass_id;
  if (!passId) throw new Error("source-tree selector missing pass_id");

  // Prefer the cache if already populated for this pass — both for normal
  // re-Open within the same producer process AND for restart recovery
  // (the recovery scan rehydrates the cache from the persisted zone_plan
  // row). Only re-partition when the cache is empty.
  let zones = ctx.partitionCache.getZonePlan(passId);
  if (!zones || zones.length === 0) {
    const partitioning = ctx.config.partitioning;
    zones = partitionIntoZones({
      projectRoot: ctx.repoRoot,
      maxFilesPerZone: partitioning.max_files_per_zone,
      smallGroupThreshold: partitioning.small_group_threshold,
      ignorePatterns: [
        "node_modules",
        ".git",
        "dist",
        ".crimefinder",
        "build",
        "coverage",
        ...partitioning.additional_ignore_patterns,
      ],
    });
    ctx.partitionCache.setZonePlan(passId, zones);
    // Persist so a producer crash mid-pass can rehydrate the SAME zone
    // IDs / labels / files at startup. Recomputation against the working
    // tree could assign different IDs if the tree shifted, which would
    // detach in-flight findings from their zones.
    await ctx.store.appendZonePlan({
      kind: "zone_plan",
      id: generateRowId(),
      ts: new Date().toISOString(),
      pass_id: passId,
      zones: zones.map((z) => ({ id: z.id, label: z.label, files: z.files })),
    });
  }

  // The source-tree fan-out parent does not call typed-state itself — all
  // gate calls happen from the zone-scoped sub-claims SplitScope produces.
  // Returning a no-op address makes that contract explicit and prevents an
  // unbound session-token from leaking into the parent claim's address.
  const address = NoOpAddressSchema.parse({
    kind: "no-op",
    pass_id: passId,
    note: "source-tree parent has no typed-state surface",
  });
  const addressBytes = new TextEncoder().encode(JSON.stringify(address));
  const payloadBytes = new TextEncoder().encode(JSON.stringify({ zone_count: zones.length }));
  const scopeBytes = new TextEncoder().encode(JSON.stringify({ kind: "source-tree", pass_id: passId }));
  return { address: addressBytes, payload: payloadBytes, scope: scopeBytes };
}
