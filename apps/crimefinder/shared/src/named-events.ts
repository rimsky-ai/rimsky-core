import { z } from "zod";

// Spec lines 584-595: the executor's startup ObservabilityCapabilities
// MUST declare all twelve names. Rimsky validates declared_events at
// template registration; missing names would block templates that
// `subscribe:` to those events. Producer-side lifecycle events
// (`pass_opened`, `pass_closed`, `zone_started`, `finding_dedup_marked`)
// are emitted from the executor on the appropriate gate calls so
// downstream consumers see a single named-event stream.
export const NAMED_EVENT_NAMES = [
  "pass_opened",
  "pass_closed",
  "zone_started",
  "zone_completed",
  "zone_skipped",
  "finding_emitted",
  "finding_resolved",
  "finding_deferred",
  "finding_dedup_marked",
  "tests_ran",
  "commit_failed",
  "help_requested",
] as const;
export type NamedEventName = (typeof NAMED_EVENT_NAMES)[number];

export const NamedEventEnvelopeSchema = z.object({
  event: z.enum(NAMED_EVENT_NAMES),
  pass_id: z.string(),
  zone_id: z.string().optional(),
  session_id: z.string().optional(),
  ts: z.string().datetime({ offset: true }),
  data: z.record(z.unknown()),
});
export type NamedEventEnvelope = z.infer<typeof NamedEventEnvelopeSchema>;

// Per-event data shapes (loose; data is structurally inert in rimsky).
export const FindingEmittedDataSchema = z.object({
  finding_id: z.string(),
  effective_class: z.union([z.literal(1), z.literal(2), z.literal(3), z.literal(4), z.literal("5a"), z.literal("5b")]),
  auto_rerouted: z.boolean(),
  file: z.string(),
});
export const FindingResolvedDataSchema = z.object({
  finding_id: z.string(),
  commit_sha: z.string(),
  iter_num: z.number().int().nullable().optional(),
});
export const FindingDeferredDataSchema = z.object({
  finding_id: z.string(),
  reason: z.string(),
});
export const TestsRanDataSchema = z.object({
  exit_code: z.number().int(),
  cached: z.boolean(),
});
export const CommitFailedDataSchema = z.object({
  finding_id: z.string(),
  stderr_excerpt: z.string(),
});
export const HelpRequestedDataSchema = z.object({
  help_id: z.string(),
  question: z.string(),
});
export const ZoneSkippedDataSchema = z.object({
  reason: z.string(),
});
export const ZoneCompletedDataSchema = z.object({
  findings_recorded: z.number().int(),
  coverage_pct: z.number(),
});
export const PassOpenedDataSchema = z.object({
  mission: z.string().optional(),
});
export const PassClosedDataSchema = z.object({
  exit_reason: z.string().optional(),
  // Pass-summary fields populated by review-complete from the
  // producer's `pass_summary` payload (carried on
  // GetZoneCoverageResponse). Mirrors `PassFinishedRow` so subscribers
  // see the same shape as the canonical JSONL row. All optional —
  // sub-threshold completions emit no summary.
  zones_planned: z.number().int().nonnegative().optional(),
  zones_completed: z.number().int().nonnegative().optional(),
  zones_skipped: z.number().int().nonnegative().optional(),
  findings_emitted: z.number().int().nonnegative().optional(),
  findings_resolved: z.number().int().nonnegative().optional(),
  findings_deferred: z.number().int().nonnegative().optional(),
  coverage_pct: z.number().optional(),
});
export const ZoneStartedDataSchema = z.object({
  zone_label: z.string().optional(),
  mission: z.string().optional(),
});
export const FindingDedupMarkedDataSchema = z.object({
  finding_id: z.string(),
  duplicate_of: z.string(),
  skipped_due_to_conflict: z.boolean().optional(),
});

export function makeNamedEvent(
  event: NamedEventName,
  args: {
    passId: string;
    zoneId?: string;
    sessionId?: string;
    ts?: string;
    data: Record<string, unknown>;
  },
): NamedEventEnvelope {
  const env: NamedEventEnvelope = {
    event,
    pass_id: args.passId,
    ts: args.ts ?? new Date().toISOString(),
    data: args.data,
  };
  if (args.zoneId !== undefined) env.zone_id = args.zoneId;
  if (args.sessionId !== undefined) env.session_id = args.sessionId;
  return NamedEventEnvelopeSchema.parse(env);
}

export function encodeEventPayload(env: NamedEventEnvelope): Uint8Array {
  return new TextEncoder().encode(JSON.stringify(env));
}
