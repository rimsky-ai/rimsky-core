import { encodeScopeIdentity } from "@crimefinder/shared";
import type { OpenContext } from "../scopes/types.js";
import { materializeFindings } from "../state/materialize.js";

export interface SubScope {
  scopeData: Uint8Array;
  partitionKey: string;
  producerMetadata: Uint8Array;
}

export interface SplitScopeArgs {
  parentClaimHandleId: string;
  parentScope: Uint8Array;
  partitionRequest: Uint8Array;
  ctx: OpenContext;
}

export async function splitScope(args: SplitScopeArgs): Promise<SubScope[]> {
  const reqText = new TextDecoder().decode(args.partitionRequest);
  let req: { kind?: string; pass_id?: string; iter_num?: number };
  try {
    req = JSON.parse(reqText);
  } catch (e) {
    throw new Error(`malformed partition_request bytes: ${(e as Error).message}`);
  }
  const passId = req.pass_id;
  if (!passId) throw new Error("partition_request missing pass_id");
  switch (req.kind) {
    case "source-tree-partition":
      return splitSourceTree(args, passId);
    case "dedup-partition":
      return splitDedup(args, passId);
    case "fix-partition":
      return splitAffected(args, passId, Number(req.iter_num), "fix-partition", "fix-cycle");
    case "re-review-partition":
      return splitAffected(args, passId, Number(req.iter_num), "re-review-partition", "re-review");
    default:
      throw new Error(`unknown partition_request kind: ${req.kind ?? "<missing>"}`);
  }
}

function splitSourceTree(args: SplitScopeArgs, passId: string): SubScope[] {
  const zones = args.ctx.partitionCache.getZonePlan(passId);
  if (!zones) {
    args.ctx.logger.warn({ passId }, "split_source_tree_no_cached_plan");
    return [];
  }
  return zones.map((z) => ({
    scopeData: encodeScopeIdentity({
      kind: "source-tree-zone",
      pass_id: passId,
      zone_id: z.id,
      zone_files: z.files,
      role: "review-zone",
    }),
    partitionKey: z.id,
    producerMetadata: new TextEncoder().encode(
      JSON.stringify({ zone_label: z.label, repo_root: args.ctx.repoRoot }),
    ),
  }));
}

function splitDedup(args: SplitScopeArgs, passId: string): SubScope[] {
  const batches = args.ctx.partitionCache.getDedupBatches(passId);
  if (!batches) {
    args.ctx.logger.warn({ passId }, "split_dedup_no_cached_batches");
    return [];
  }
  return batches.map((batch, i) => ({
    scopeData: encodeScopeIdentity({
      kind: "dedup-batch",
      pass_id: passId,
      batch_index: i,
      files: batch.map((g) => g.file),
    }),
    partitionKey: `dedup-batch-${i}`,
    producerMetadata: new TextEncoder().encode(
      JSON.stringify({ batch_index: i, file_groups: batch }),
    ),
  }));
}

async function splitAffected(
  args: SplitScopeArgs,
  passId: string,
  iterNum: number,
  label: string,
  role: "fix-cycle" | "re-review",
): Promise<SubScope[]> {
  if (!Number.isFinite(iterNum)) {
    throw new Error(`${label} requires numeric iter_num`);
  }
  const affected = args.ctx.partitionCache.getAffectedZones(passId, iterNum);
  if (!affected) {
    args.ctx.logger.warn({ passId, iterNum, label }, "split_affected_no_cached_set");
    return [];
  }

  // For fix-cycle children, project the cached findings_by_zone map onto
  // each child so the per-zone finding IDs travel inline on the address.
  // Rimsky does not substitute inside userdata (see
  // `runtime/userdata_overrides.go` deep-merge-only;
  // `graph/attribute/doc.go` §spec invariant 11), so the per-child IDs
  // must live on the scope identity / address — the only producer-owned
  // surface that flows from SplitScope through openFanOutChild to the
  // dispatched executor. Re-review children have no fix payload so the
  // map stays empty.
  //
  // After computing the map, fix-cycle drops zones whose bucket is empty:
  // those zones have no unresolved class-1-4 findings to fix (everything
  // is already 5a/5b, fixed, or deferred), so dispatching an agent there
  // would just feed it an empty assigned_findings payload. Re-review keeps
  // every affected zone — it doesn't carry per-zone IDs.
  let findingsByZone: Record<string, string[]> = {};
  let dispatched = affected;
  if (role === "fix-cycle") {
    const findingsRows = await args.ctx.store.readFindings();
    const materialized = materializeFindings(findingsRows);
    for (const z of affected) findingsByZone[z.id] = [];
    for (const { row, status } of materialized.values()) {
      if (row.pass_id !== passId) continue;
      const cls = row.effective_class;
      if (cls === "5a" || cls === "5b") continue;
      if (status !== "open" && status !== "fixing") continue;
      const bucket = findingsByZone[row.zone_id];
      if (bucket) bucket.push(row.id);
    }
    dispatched = affected.filter((z) => (findingsByZone[z.id] ?? []).length > 0);
  }

  return dispatched.map((z) => ({
    scopeData: encodeScopeIdentity({
      kind: "source-tree-zone",
      pass_id: passId,
      zone_id: z.id,
      zone_files: z.files,
      role,
      iter_num: iterNum,
      assigned_finding_ids:
        role === "fix-cycle" ? (findingsByZone[z.id] ?? []) : undefined,
    }),
    partitionKey: z.id,
    producerMetadata: new TextEncoder().encode(
      JSON.stringify({
        zone_label: z.label,
        repo_root: args.ctx.repoRoot,
        iter_num: iterNum,
        partition_kind: label,
        role,
      }),
    ),
  }));
}
