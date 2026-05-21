import { z } from "zod";

export interface ToolDefinition {
  name: string;
  description: string;
  inputSchema: z.ZodTypeAny;
}

// Auth has moved to the MCP transport (Authorization: Bearer <token>
// header injected by the executor's MCP client config). Tool inputs no
// longer carry a token field — agents that include one anyway will see
// the field ignored.
const findingClass = z.union([
  z.literal(1),
  z.literal(2),
  z.literal(3),
  z.literal(4),
  z.literal("5a"),
  z.literal("5b"),
]);

export const ReviewContextInput = z.object({});
export const ReviewFindingInput = z.object({
  class: findingClass,
  file: z.string(),
  line_start: z.number().int().nullable().optional(),
  line_end: z.number().int().nullable().optional(),
  symbol: z.string().optional(),
  description: z.string(),
  concept_slug: z.string().nullable().optional(),
  tension_slug: z.string().nullable().optional(),
  confidence: z.enum(["high", "low"]),
});
export const ReviewCoverageInput = z.object({
  files_read: z.array(z.string()),
});
export const ReviewCompleteInput = z.object({});
export const ReviewRunTestsInput = z.object({});
export const ReviewCommitFixInput = z.object({
  finding_id: z.string(),
  fix_description: z.string(),
  commit_message: z.string(),
});
export const ReviewDeferInput = z.object({
  finding_id: z.string(),
  reason: z.string(),
});
export const ReviewSkipZoneInput = z.object({
  reason: z.string(),
});
export const ReviewRequestHelpInput = z.object({
  question: z.string(),
  blocker_finding_id: z.string().optional(),
});
export const ReviewDedupMarkInput = z.object({
  finding_id: z.string(),
  duplicate_of: z.string(),
});

// TOOL_DEFINITIONS is the single source of truth for both the MCP server's
// tool registration and any other consumer that wants to introspect the
// vocabulary (e.g. the executor's capabilities export). Descriptions and
// schemas live ONLY here.
export const TOOL_DEFINITIONS: ToolDefinition[] = [
  {
    name: "review_context",
    description:
      "Load this session's mission, files, concept docs, and existing findings.",
    inputSchema: ReviewContextInput,
  },
  {
    name: "review_finding",
    description: "Emit a finding. class is 1..4 (numeric) or '5a'/'5b'.",
    inputSchema: ReviewFindingInput,
  },
  {
    name: "review_coverage",
    description: "Report which files you've actually read so far.",
    inputSchema: ReviewCoverageInput,
  },
  {
    name: "review_complete",
    description: "Mark this session terminal. Call exactly once.",
    inputSchema: ReviewCompleteInput,
  },
  {
    name: "review_run_tests",
    description: "Run the configured test command. Result is cached by tree mtime.",
    inputSchema: ReviewRunTestsInput,
  },
  {
    name: "review_commit_fix",
    description: "Atomically commit the fix for one finding. Working tree must be dirty.",
    inputSchema: ReviewCommitFixInput,
  },
  {
    name: "review_defer",
    description: "Mark a finding as deferred with a reason.",
    inputSchema: ReviewDeferInput,
  },
  {
    name: "review_skip_zone",
    description: "Skip this zone with a reason (counts toward partial-coverage report).",
    inputSchema: ReviewSkipZoneInput,
  },
  {
    name: "review_request_help",
    description: "Record a help request the operator can review.",
    inputSchema: ReviewRequestHelpInput,
  },
  {
    name: "review_dedup_mark",
    description:
      "Mark a finding as a duplicate of another finding (dedup sessions only).",
    inputSchema: ReviewDedupMarkInput,
  },
];

export const TOOL_NAMES = TOOL_DEFINITIONS.map((d) => d.name);
