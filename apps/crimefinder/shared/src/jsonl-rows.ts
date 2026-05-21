import { z } from "zod";

// Finding class is a discriminated union: numeric 1-4 OR the strings "5a"/"5b".
export const FindingClassSchema = z.union([
  z.literal(1),
  z.literal(2),
  z.literal(3),
  z.literal(4),
  z.literal("5a"),
  z.literal("5b"),
]);
export type FindingClassValue = z.infer<typeof FindingClassSchema>;

// ----- findings.jsonl row kinds -----

export const FindingRowSchema = z.object({
  kind: z.literal("finding"),
  id: z.string(),
  ts: z.string().datetime({ offset: true }),
  pass_id: z.string(),
  zone_id: z.string(),
  session_id: z.string(),
  class: FindingClassSchema,
  effective_class: FindingClassSchema,
  auto_rerouted: z.boolean(),
  file: z.string(),
  line_start: z.number().int().nullable(),
  line_end: z.number().int().nullable(),
  symbol: z.string().optional(),
  description: z.string(),
  fingerprint: z.string(),
  concept_slug: z.string().nullable(),
  tension_slug: z.string().nullable(),
  confidence: z.enum(["high", "low"]),
  status: z.literal("open"),
  originating_zone_id: z.string().nullable(),
});
export type FindingRow = z.infer<typeof FindingRowSchema>;

export const StatusUpdateRowSchema = z.object({
  kind: z.literal("status_update"),
  id: z.string(),
  ts: z.string().datetime({ offset: true }),
  ref: z.string(),
  status: z.enum([
    "fixing",
    "fixed",
    "deferred",
    "duplicate-of",
    "void",
    "queued-to-spec",
    "resolved-via-spec",
  ]),
  by_pass: z.string(),
  by_session: z.string(),
  resolved_at_commit: z.string().nullable().optional(),
  duplicate_of: z.string().nullable().optional(),
  reason: z.string().optional(),
  note: z.string().optional(),
});
export type StatusUpdateRow = z.infer<typeof StatusUpdateRowSchema>;

export const TensionConfirmationRowSchema = z.object({
  kind: z.literal("tension_confirmation"),
  id: z.string(),
  ts: z.string().datetime({ offset: true }),
  pass_id: z.string(),
  zone_id: z.string(),
  tension_slug: z.string(),
  file: z.string(),
  description: z.string(),
});
export type TensionConfirmationRow = z.infer<typeof TensionConfirmationRowSchema>;

export const HelpRequestRowSchema = z.object({
  kind: z.literal("help_request"),
  id: z.string(),
  ts: z.string().datetime({ offset: true }),
  pass_id: z.string(),
  session_id: z.string(),
  question: z.string(),
  blocker_finding_id: z.string().nullable().optional(),
  status: z.literal("open"),
});
export type HelpRequestRow = z.infer<typeof HelpRequestRowSchema>;

export const FindingsRowSchema = z.discriminatedUnion("kind", [
  FindingRowSchema,
  StatusUpdateRowSchema,
  TensionConfirmationRowSchema,
  HelpRequestRowSchema,
]);
export type FindingsRow = z.infer<typeof FindingsRowSchema>;

// ----- coverage.jsonl -----

export const CoverageRowSchema = z.object({
  ts: z.string().datetime({ offset: true }),
  pass_id: z.string(),
  session_id: z.string(),
  zone_id: z.string(),
  file: z.string(),
});
export type CoverageRow = z.infer<typeof CoverageRowSchema>;

// ----- passes.jsonl -----

export const PassStartedRowSchema = z.object({
  kind: z.literal("pass_started"),
  id: z.string(),
  ts: z.string().datetime({ offset: true }),
  mission: z.string(),
  trigger: z.enum(["manual", "cron", "webhook", "concept_edit_watch"]),
  trigger_metadata: z.record(z.unknown()).optional(),
  template_hash: z.string(),
  fix_cycle_cap: z.number().int().positive(),
  params_hash: z.string(),
});
export type PassStartedRow = z.infer<typeof PassStartedRowSchema>;

export const PassFinishedRowSchema = z.object({
  kind: z.literal("pass_finished"),
  ref: z.string(),
  ts: z.string().datetime({ offset: true }),
  exit_reason: z.enum(["complete", "interrupted", "failed", "partial"]),
  zones_planned: z.number().int().nonnegative(),
  zones_completed: z.number().int().nonnegative(),
  zones_skipped: z.number().int().nonnegative(),
  findings_emitted: z.number().int().nonnegative(),
  findings_resolved: z.number().int().nonnegative(),
  findings_deferred: z.number().int().nonnegative(),
  findings_class_5_remaining_open: z.number().int().nonnegative(),
  fix_cycle_iterations_run: z.number().int().nonnegative(),
  coverage_pct: z.number(),
  commits: z.array(z.string()),
});
export type PassFinishedRow = z.infer<typeof PassFinishedRowSchema>;

// Durable per-pass iteration counter (extension; see T17). The optional
// `claim_id` lets the producer make iter advance idempotent against
// rimsky retries of the same `Open` — see iteration-counter.ts.
export const IterMarkerRowSchema = z.object({
  kind: z.literal("iter_marker"),
  id: z.string(),
  ts: z.string().datetime({ offset: true }),
  pass_id: z.string(),
  iter_num: z.number().int().positive(),
  claim_id: z.string().optional(),
});
export type IterMarkerRow = z.infer<typeof IterMarkerRowSchema>;

// Skip-zone (extension; persisted alongside passes rows).
export const SkipZoneRowSchema = z.object({
  kind: z.literal("skip_zone"),
  id: z.string(),
  ts: z.string().datetime({ offset: true }),
  pass_id: z.string(),
  zone_id: z.string(),
  session_id: z.string(),
  reason: z.string(),
});
export type SkipZoneRow = z.infer<typeof SkipZoneRowSchema>;

// pass_closed_emitted is the producer's de-dup marker for the
// `pass_closed` named-event. handleGetZoneCoverage writes one such row
// under the passes-file mutex iff (a) the pass meets the completion
// gate this call AND (b) no row already exists for the pass_id. The
// "first writer wins" handshake means at most one concurrent
// completion observes `pass_complete:true`, so consumers never receive
// duplicate `pass_closed` events.
export const PassClosedEmittedRowSchema = z.object({
  kind: z.literal("pass_closed_emitted"),
  id: z.string(),
  ts: z.string().datetime({ offset: true }),
  pass_id: z.string(),
});
export type PassClosedEmittedRow = z.infer<typeof PassClosedEmittedRowSchema>;

// Zone-plan row: the producer writes this once per pass after partitioning,
// so a producer crash mid-pass can rehydrate the zone IDs / labels / files
// rather than recomputing (a recomputation would assign different IDs if
// the tree changed under the partitioner). With the zone plan persisted,
// post-restart findings keep their zone_id stable and the final report's
// per-zone numbers stay accurate.
//
// `seq` is a per-pass monotonic write counter the producer assigns under
// the passes-file mutex. Recovery scans use `seq` (not `ts`) for the
// last-wins ordering across multiple zone_plan rows for the same pass —
// ISO-8601 millisecond timestamps can tie under high throughput, which
// would let an older plan win and detach downstream findings from
// current zone IDs. Optional for backward-compat with legacy rows; the
// scan falls back to `ts` when `seq` is absent.
export const ZonePlanRowSchema = z.object({
  kind: z.literal("zone_plan"),
  id: z.string(),
  ts: z.string().datetime({ offset: true }),
  pass_id: z.string(),
  seq: z.number().int().nonnegative().optional(),
  zones: z.array(
    z.object({
      id: z.string(),
      label: z.string(),
      files: z.array(z.string()),
    }),
  ),
});
export type ZonePlanRow = z.infer<typeof ZonePlanRowSchema>;

// Dedup-batches row: the producer writes this once per pass when the
// dedup-grouping parent scope opens, so a producer crash mid-dedup can
// rehydrate the SAME batch layout at startup. Without this, dedup-batch
// sub-claims that survive a restart would resolve their
// `getReviewContext` payload with an empty `file_groups` (the in-memory
// partition cache having been wiped), and the agent would have nothing
// to deduplicate.
export const DedupBatchesRowSchema = z.object({
  kind: z.literal("dedup_batches"),
  id: z.string(),
  ts: z.string().datetime({ offset: true }),
  pass_id: z.string(),
  // Per-pass monotonic write counter; see ZonePlanRowSchema.seq for the
  // ordering rationale. Optional for backward-compat.
  seq: z.number().int().nonnegative().optional(),
  batches: z.array(
    z.array(
      z.object({
        file: z.string(),
        finding_ids: z.array(z.string()),
      }),
    ),
  ),
});
export type DedupBatchesRow = z.infer<typeof DedupBatchesRowSchema>;

export const PassesRowSchema = z.discriminatedUnion("kind", [
  PassStartedRowSchema,
  PassFinishedRowSchema,
  IterMarkerRowSchema,
  SkipZoneRowSchema,
  ZonePlanRowSchema,
  DedupBatchesRowSchema,
  PassClosedEmittedRowSchema,
]);
export type PassesRow = z.infer<typeof PassesRowSchema>;

// ----- helpers -----

export function parseFindingsLine(line: string): FindingsRow {
  const obj = JSON.parse(line);
  return FindingsRowSchema.parse(obj);
}

export function serializeFindingsRow(row: FindingsRow): string {
  // Validate before serialization to keep round-trip honest.
  FindingsRowSchema.parse(row);
  return JSON.stringify(row);
}

export function parsePassesLine(line: string): PassesRow {
  const obj = JSON.parse(line);
  return PassesRowSchema.parse(obj);
}

export function serializePassesRow(row: PassesRow): string {
  PassesRowSchema.parse(row);
  return JSON.stringify(row);
}

export function parseCoverageLine(line: string): CoverageRow {
  const obj = JSON.parse(line);
  return CoverageRowSchema.parse(obj);
}

export function serializeCoverageRow(row: CoverageRow): string {
  CoverageRowSchema.parse(row);
  return JSON.stringify(row);
}
