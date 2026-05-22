import {
  PassStateAddressSchema,
  SourceTreeZoneAddressSchema,
  DedupBatchAddressSchema,
} from "@crimefinder/shared";
import type { OpenContext, OpenResult } from "../scopes/types.js";
import { openPassState } from "../scopes/pass-state.js";
import { openContextScan } from "../scopes/context-scan.js";
import { openSourceTree } from "../scopes/source-tree.js";
import { openAggregateFindings } from "../scopes/aggregate-findings.js";
import { openDedupGrouping } from "../scopes/dedup-grouping.js";
import { openClassSplit } from "../scopes/class-split.js";
import { openUnresolvedClass14 } from "../scopes/unresolved-class-1-4.js";
import { openFixPartition } from "../scopes/fix-partition.js";
import { openReReviewPartition } from "../scopes/re-review-partition.js";
import { openIterAggregate } from "../scopes/iter-aggregate.js";
import { openClass5Finalize } from "../scopes/class-5-finalize.js";
import { openReport } from "../scopes/report.js";

export interface OpenRequest {
  selector: string;
  claim_id: string;
  scope_data?: Uint8Array | null;
}

export interface OpenResponseAcquired {
  type: "acquired";
  address: Uint8Array;
  payload: Uint8Array;
  scope: Uint8Array;
  realized_write_semantics: "WRITE_SEMANTICS_SYNC";
}
export interface OpenResponseUnavailable {
  type: "unavailable";
  message: string;
}
export type OpenResponse = OpenResponseAcquired | OpenResponseUnavailable;

const PREFIX_HANDLERS: Array<[string, (ctx: OpenContext) => Promise<OpenResult>]> = [
  ["@pass-state:", openPassState],
  ["@context-scan:", openContextScan],
  ["@source-tree:", openSourceTree],
  ["@aggregate-findings:", openAggregateFindings],
  ["@dedup-grouping:", openDedupGrouping],
  ["@class-split:", openClassSplit],
  ["@unresolved-class-1-4:", openUnresolvedClass14],
  ["@fix-partition:", openFixPartition],
  ["@re-review-partition:", openReReviewPartition],
  ["@iter-aggregate:", openIterAggregate],
  ["@class-5-finalize:", openClass5Finalize],
  ["@report:", openReport],
];

// Fan-out child Open: rimsky passes back the producer-canonicalized
// scope_data (from SplitScope) for the child's Open. The selector is
// empty/unspecified; we identify by parsing scope_data.
async function openFanOutChild(
  scopeBytes: Uint8Array,
  ctx: OpenContext,
): Promise<OpenResult> {
  const text = new TextDecoder().decode(scopeBytes);
  const parsed = JSON.parse(text);
  if (parsed.kind === "source-tree-zone") {
    // Role lives on the scope identity (set by SplitScope on the parent's
    // partition) — without it, downstream gates can't tell apart review-zone
    // vs fix-cycle vs re-review children. Default to "review-zone" for
    // legacy callers (e.g. tests issuing tokens directly).
    const role: "review-zone" | "fix-cycle" | "re-review" =
      parsed.role === "fix-cycle" || parsed.role === "re-review"
        ? parsed.role
        : "review-zone";
    const sessionToken = ctx.tokens.issue({
      passId: parsed.pass_id,
      claimHandleId: ctx.claimId,
      zoneId: parsed.zone_id,
      role,
      issuedAt: Date.now(),
    });
    // For fix-cycle / re-review children, copy iter_num and
    // assigned_finding_ids straight off the scopeData built by SplitScope.
    // Rimsky doesn't substitute inside attribute `default:` values, so
    // these per-child values can only reach the executor via the address
    // bytes.
    const address = SourceTreeZoneAddressSchema.parse({
      kind: "source-tree-zone",
      pass_id: parsed.pass_id,
      zone_id: parsed.zone_id,
      zone_label: parsed.zone_label ?? parsed.zone_id,
      zone_files: parsed.zone_files,
      repo_root_path: ctx.repoRoot,
      state_endpoint_url: ctx.stateEndpointUrl,
      session_token: sessionToken,
      iter_num:
        typeof parsed.iter_num === "number" && Number.isFinite(parsed.iter_num)
          ? parsed.iter_num
          : undefined,
      assigned_finding_ids: Array.isArray(parsed.assigned_finding_ids)
        ? parsed.assigned_finding_ids.filter((s: unknown): s is string => typeof s === "string")
        : undefined,
    });
    return {
      address: new TextEncoder().encode(JSON.stringify(address)),
      payload: new TextEncoder().encode(
        JSON.stringify({ zone_id: parsed.zone_id, file_count: parsed.zone_files.length }),
      ),
      scope: scopeBytes,
    };
  }
  if (parsed.kind === "dedup-batch") {
    // Dedup-batch session: bind token with role="dedup" and batch_index so
    // handleGetReviewContext can return the per-batch file_groups. The
    // address also carries the file_groups for executors that read them
    // directly off the dispatch, but the gate-side lookup is the canonical
    // path (consumers shouldn't have to re-parse address bytes).
    const sessionToken = ctx.tokens.issue({
      passId: parsed.pass_id,
      claimHandleId: ctx.claimId,
      role: "dedup",
      batchIndex: parsed.batch_index,
      issuedAt: Date.now(),
    });
    const batches = ctx.partitionCache.getDedupBatches(parsed.pass_id) ?? [];
    const batch = batches[parsed.batch_index] ?? [];
    const fileGroups = batch.map((g) => ({
      file: g.file,
      finding_ids: g.findingIds ?? [],
    }));
    const address = DedupBatchAddressSchema.parse({
      kind: "dedup-batch",
      pass_id: parsed.pass_id,
      batch_index: parsed.batch_index,
      file_groups: fileGroups,
      state_endpoint_url: ctx.stateEndpointUrl,
      session_token: sessionToken,
    });
    return {
      address: new TextEncoder().encode(JSON.stringify(address)),
      payload: new TextEncoder().encode(
        JSON.stringify({
          batch_index: parsed.batch_index,
          file_count: Array.isArray(parsed.files) ? parsed.files.length : 0,
        }),
      ),
      scope: scopeBytes,
    };
  }
  throw new Error(`unrecognized fan-out child scope kind: ${parsed.kind}`);
}

export async function handleOpen(req: OpenRequest, ctx: OpenContext): Promise<OpenResponse> {
  // Fan-out child Open: scope_data is supplied (non-empty), selector typically empty.
  if (req.scope_data && req.scope_data.length > 0 && (!req.selector || req.selector === "")) {
    try {
      const r = await openFanOutChild(req.scope_data, ctx);
      return {
        type: "acquired",
        address: r.address,
        payload: r.payload,
        scope: r.scope,
        realized_write_semantics: "WRITE_SEMANTICS_SYNC",
      };
    } catch (e) {
      ctx.logger.warn({ err: String(e) }, "fan_out_child_open_failed");
      return { type: "unavailable", message: String(e) };
    }
  }

  for (const [prefix, fn] of PREFIX_HANDLERS) {
    if (req.selector.startsWith(prefix)) {
      try {
        const r = await fn(ctx);
        // Ensure addresses validate; throw if not.
        if (r.address.length > 0) {
          const parsed = JSON.parse(new TextDecoder().decode(r.address));
          if (parsed.kind === "pass-state") PassStateAddressSchema.parse(parsed);
          if (parsed.kind === "source-tree-zone") SourceTreeZoneAddressSchema.parse(parsed);
        }
        return {
          type: "acquired",
          address: r.address,
          payload: r.payload,
          scope: r.scope,
          realized_write_semantics: "WRITE_SEMANTICS_SYNC",
        };
      } catch (e) {
        ctx.logger.warn({ selector: req.selector, err: String(e) }, "scope_handler_failed");
        return { type: "unavailable", message: String(e) };
      }
    }
  }
  ctx.logger.warn({ selector: req.selector }, "open_unknown_selector");
  return { type: "unavailable", message: `unknown selector: ${req.selector}` };
}
