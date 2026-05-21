import { z } from "zod";

export const SourceTreeZoneAddressSchema = z.object({
  kind: z.literal("source-tree-zone"),
  pass_id: z.string(),
  zone_id: z.string(),
  zone_label: z.string(),
  zone_files: z.array(z.string()),
  repo_root_path: z.string(),
  state_endpoint_url: z.string(),
  session_token: z.string(),
  // For fix-cycle / re-review children: the iteration this dispatch
  // belongs to, and (fix-cycle only) the per-zone finding IDs the agent
  // owns. These travel in the address — NOT in userdata — because rimsky
  // does not run `{{...}}` substitution inside userdata (see
  // `runtime/userdata_overrides.go` deep-merge-only; `graph/attribute/doc.go`
  // §spec invariant 11; `.ok-planner/design/_discover/2026-05-10-opacity-of-userdata-claim-blob.md`).
  // The producer's SplitScope writes them into per-child scopeData and
  // openFanOutChild copies them into the SourceTreeZoneAddress; the
  // executor reads them off `primary` in agent-run.ts.
  iter_num: z.number().int().positive().optional(),
  assigned_finding_ids: z.array(z.string()).optional(),
});
export type SourceTreeZoneAddress = z.infer<typeof SourceTreeZoneAddressSchema>;

export const PassStateAddressSchema = z.object({
  kind: z.literal("pass-state"),
  pass_id: z.string(),
  state_endpoint_url: z.string(),
  session_token: z.string(),
});
export type PassStateAddress = z.infer<typeof PassStateAddressSchema>;

// Returned by parent-only fan-out scopes (e.g. source-tree) that have no
// use for typed-state themselves — their holders never call CrimefinderState
// gates. The empty bytes signal "no typed-state on this scope" to consumers
// of the address; sub-scope holders receive their own zone or dedup-batch
// address via SplitScope.
export const NoOpAddressSchema = z.object({
  kind: z.literal("no-op"),
  pass_id: z.string(),
  note: z.string(),
});
export type NoOpAddress = z.infer<typeof NoOpAddressSchema>;

export const DedupBatchAddressSchema = z.object({
  kind: z.literal("dedup-batch"),
  pass_id: z.string(),
  batch_index: z.number().int(),
  file_groups: z.array(z.object({ file: z.string(), finding_ids: z.array(z.string()) })),
  state_endpoint_url: z.string(),
  session_token: z.string(),
});
export type DedupBatchAddress = z.infer<typeof DedupBatchAddressSchema>;

export const ScopeAddressSchema = z.discriminatedUnion("kind", [
  SourceTreeZoneAddressSchema,
  PassStateAddressSchema,
  DedupBatchAddressSchema,
  NoOpAddressSchema,
]);
export type ScopeAddress = z.infer<typeof ScopeAddressSchema>;

export function encodeAddress(a: ScopeAddress): Uint8Array {
  ScopeAddressSchema.parse(a);
  return new TextEncoder().encode(JSON.stringify(a));
}

export function decodeAddress(bytes: Uint8Array): ScopeAddress {
  const text = new TextDecoder().decode(bytes);
  const obj = JSON.parse(text);
  return ScopeAddressSchema.parse(obj);
}

// Scope-identity bytes (used by ScopesConflict). Distinct from the *address*
// shape — these are the bytes ClaimProducer.Open returns as `scope` to
// uniquely identify what was acquired. Byte-equal scopes ↔ same identity.

// `role` identifies which mission the child session is for: review-zone,
// fix-cycle, or re-review. The producer's SplitScope encodes the role on
// every zone sub-scope so the fan-out child Open can issue a session-token
// tagged with the right role (without this, source-tree-zone tokens have
// no role and downstream gate guards can't tell apart review/fix/re-review).
// Role does NOT participate in scope-identity collision: two source-tree-zone
// scopes with the same pass_id and zone_id but different roles still collide
// at the ScopesConflict layer (per concept:scope-identity), so a fix-cycle
// session can hold a zone the review pass already cleared without re-claiming.
export const ZoneScopeIdentitySchema = z.object({
  kind: z.literal("source-tree-zone"),
  pass_id: z.string(),
  zone_id: z.string(),
  zone_files: z.array(z.string()),
  role: z.enum(["review-zone", "fix-cycle", "re-review"]).optional(),
  // Carried on scopeData so the producer's openFanOutChild can copy them
  // into the per-child SourceTreeZoneAddress without re-reading the
  // partition cache. Optional because review-zone children have no
  // iteration semantics. ScopesConflict ignores these fields — they
  // never participate in conflict detection.
  iter_num: z.number().int().positive().optional(),
  assigned_finding_ids: z.array(z.string()).optional(),
});
export type ZoneScopeIdentity = z.infer<typeof ZoneScopeIdentitySchema>;

export const PassStateScopeIdentitySchema = z.object({
  kind: z.literal("pass-state"),
  pass_id: z.string(),
});
export type PassStateScopeIdentity = z.infer<typeof PassStateScopeIdentitySchema>;

// Dedup-batch scope identity. Distinguishes dedup batches from source-tree
// zones at the ScopesConflict layer so they never collide and downstream
// consumers can dispatch on kind.
export const DedupBatchScopeIdentitySchema = z.object({
  kind: z.literal("dedup-batch"),
  pass_id: z.string(),
  batch_index: z.number().int().nonnegative(),
  files: z.array(z.string()),
});
export type DedupBatchScopeIdentity = z.infer<typeof DedupBatchScopeIdentitySchema>;

export const ScopeIdentitySchema = z.discriminatedUnion("kind", [
  ZoneScopeIdentitySchema,
  PassStateScopeIdentitySchema,
  DedupBatchScopeIdentitySchema,
]);
export type ScopeIdentity = z.infer<typeof ScopeIdentitySchema>;

export function encodeScopeIdentity(s: ScopeIdentity): Uint8Array {
  // Stable JSON: sort keys so byte-equality holds across structurally-equal scopes.
  return new TextEncoder().encode(stableStringify(s));
}

function stableStringify(value: unknown): string {
  if (Array.isArray(value)) {
    return "[" + value.map((v) => stableStringify(v)).join(",") + "]";
  }
  if (value !== null && typeof value === "object") {
    const obj = value as Record<string, unknown>;
    // Skip undefined-valued keys: JSON.stringify omits them at the
    // top-level, and the stable form should match (otherwise the
    // produced bytes would contain the literal token "undefined" and
    // break round-trip via JSON.parse).
    const keys = Object.keys(obj)
      .filter((k) => obj[k] !== undefined)
      .sort();
    return (
      "{" +
      keys.map((k) => JSON.stringify(k) + ":" + stableStringify(obj[k])).join(",") +
      "}"
    );
  }
  return JSON.stringify(value);
}
