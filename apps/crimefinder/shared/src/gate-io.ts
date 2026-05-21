import { z } from "zod";
import { FindingClassSchema } from "./jsonl-rows.js";

// ----- review_context -----

export const ReviewContextInputSchema = z.object({});
export type ReviewContextInput = z.infer<typeof ReviewContextInputSchema>;

export const ConceptDocSummarySchema = z.object({
  slug: z.string(),
  path: z.string(),
  content: z.string(),
});
export const OpenTensionSummarySchema = z.object({
  slug: z.string(),
  path: z.string(),
  content: z.string(),
});
export const ZoneFindingSummarySchema = z.object({
  id: z.string(),
  file: z.string(),
  class: FindingClassSchema,
  status: z.string(),
  description_summary: z.string(),
});

export const ReviewContextOutputSchema = z.discriminatedUnion("role", [
  z.object({
    role: z.literal("review-zone"),
    pass_id: z.string(),
    zone_id: z.string(),
    zone_label: z.string(),
    mission: z.string(),
    zone_files: z.array(z.string()),
    concept_docs: z.array(ConceptDocSummarySchema),
    open_tensions: z.array(OpenTensionSummarySchema),
    existing_findings_in_zone: z.array(ZoneFindingSummarySchema),
    finding_categories_help: z.string(),
    ignore_patterns: z.array(z.string()),
  }),
  z.object({
    role: z.literal("fix-cycle"),
    pass_id: z.string(),
    zone_id: z.string(),
    zone_label: z.string(),
    assigned_findings: z.array(
      z.object({
        id: z.string(),
        file: z.string(),
        line_start: z.number().int().nullable(),
        line_end: z.number().int().nullable(),
        description: z.string(),
        concept_slug: z.string().nullable(),
        tension_slug: z.string().nullable(),
        prior_fix_attempts: z.array(z.unknown()),
      }),
    ),
    test_command: z.string().nullable(),
    require_tests_before_commit: z.boolean(),
    concept_docs: z.array(ConceptDocSummarySchema),
    open_tensions: z.array(OpenTensionSummarySchema),
  }),
  z.object({
    role: z.literal("dedup"),
    pass_id: z.string(),
    file_groups: z.array(z.object({ file: z.string(), finding_ids: z.array(z.string()) })),
  }),
  z.object({
    role: z.literal("re-review"),
    pass_id: z.string(),
    zone_id: z.string(),
    zone_label: z.string(),
    zone_files: z.array(z.string()),
    iter_num: z.number().int(),
    concept_docs: z.array(ConceptDocSummarySchema),
    open_tensions: z.array(OpenTensionSummarySchema),
  }),
]);
export type ReviewContextOutput = z.infer<typeof ReviewContextOutputSchema>;

// ----- review_finding -----

export const ReviewFindingInputSchema = z.object({
  class: FindingClassSchema,
  file: z.string(),
  line_start: z.number().int().nullable().optional(),
  line_end: z.number().int().nullable().optional(),
  symbol: z.string().optional(),
  description: z.string(),
  concept_slug: z.string().nullable().optional(),
  tension_slug: z.string().nullable().optional(),
  confidence: z.enum(["high", "low"]),
});
export type ReviewFindingInput = z.infer<typeof ReviewFindingInputSchema>;

export const ReviewFindingOutputSchema = z.object({
  finding_id: z.string(),
  effective_class: FindingClassSchema,
  auto_rerouted: z.boolean(),
  tension_confirmation: z.boolean().optional(),
});
export type ReviewFindingOutput = z.infer<typeof ReviewFindingOutputSchema>;

// ----- review_coverage -----

export const ReviewCoverageInputSchema = z.object({
  files_read: z.array(z.string()),
});
export type ReviewCoverageInput = z.infer<typeof ReviewCoverageInputSchema>;

export const ReviewCoverageOutputSchema = z.object({
  recorded_count: z.number().int().nonnegative(),
});
export type ReviewCoverageOutput = z.infer<typeof ReviewCoverageOutputSchema>;

// ----- review_complete -----

export const ReviewCompleteInputSchema = z.object({});
export type ReviewCompleteInput = z.infer<typeof ReviewCompleteInputSchema>;

export const ReviewCompleteOutputSchema = z.object({
  findings_recorded: z.number().int().nonnegative(),
  coverage_pct: z.number(),
});
export type ReviewCompleteOutput = z.infer<typeof ReviewCompleteOutputSchema>;

// ----- review_run_tests -----

export const ReviewRunTestsInputSchema = z.object({});
export type ReviewRunTestsInput = z.infer<typeof ReviewRunTestsInputSchema>;

export const ReviewRunTestsOutputSchema = z.object({
  exit_code: z.number().int(),
  output_excerpt: z.string(),
  ran_at: z.string(),
  cached: z.boolean(),
});
export type ReviewRunTestsOutput = z.infer<typeof ReviewRunTestsOutputSchema>;

// ----- review_commit_fix -----

export const ReviewCommitFixInputSchema = z.object({
  finding_id: z.string(),
  fix_description: z.string(),
  commit_message: z.string(),
});
export type ReviewCommitFixInput = z.infer<typeof ReviewCommitFixInputSchema>;

export const ReviewCommitFixOutputSchema = z.object({
  commit_sha: z.string(),
  finding_status: z.literal("fixed"),
});
export type ReviewCommitFixOutput = z.infer<typeof ReviewCommitFixOutputSchema>;

// ----- review_defer -----

export const ReviewDeferInputSchema = z.object({
  finding_id: z.string(),
  reason: z.string(),
});
export type ReviewDeferInput = z.infer<typeof ReviewDeferInputSchema>;

export const ReviewDeferOutputSchema = z.object({
  finding_id: z.string(),
  finding_status: z.literal("deferred"),
});
export type ReviewDeferOutput = z.infer<typeof ReviewDeferOutputSchema>;

// ----- review_skip_zone -----

export const ReviewSkipZoneInputSchema = z.object({
  reason: z.string(),
});
export type ReviewSkipZoneInput = z.infer<typeof ReviewSkipZoneInputSchema>;

export const ReviewSkipZoneOutputSchema = z.object({
  zone_id: z.string(),
  skipped: z.literal(true),
});
export type ReviewSkipZoneOutput = z.infer<typeof ReviewSkipZoneOutputSchema>;

// ----- review_request_help -----

export const ReviewRequestHelpInputSchema = z.object({
  question: z.string(),
  blocker_finding_id: z.string().optional(),
});
export type ReviewRequestHelpInput = z.infer<typeof ReviewRequestHelpInputSchema>;

export const ReviewRequestHelpOutputSchema = z.object({
  help_id: z.string(),
});
export type ReviewRequestHelpOutput = z.infer<typeof ReviewRequestHelpOutputSchema>;
