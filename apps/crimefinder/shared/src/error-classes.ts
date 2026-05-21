// Gate-level error classes (returned in MCP error envelopes to Claude CLI).
export const GATE_ERROR_CLASSES = [
  "finding_not_found",
  "finding_already_resolved",
  "working_tree_clean",
  "working_tree_changes_out_of_scope",
  "tests_not_recent",
  "tests_failed",
  "test_command_not_configured",
  "coverage_below_threshold",
  // Raised by review_skip_zone when the agent tries to skip a zone whose
  // coverage already meets/exceeds the threshold. Distinct from
  // `coverage_below_threshold` (which has the opposite meaning) so an
  // agent branching on error class can disambiguate "skip refused — keep
  // reviewing" from "complete refused — read more or skip".
  "coverage_above_threshold",
  "unresolved_findings_in_flight",
  "concept_citation_missing",
  "commit_failed",
  "tension_already_cataloged",
  // Raised when a session-token holds the wrong role for the gate it tried
  // to invoke (e.g. dedup session calling review_run_tests, or a session
  // calling review_skip_zone without a zone bound to the token).
  "wrong_session_role",
  // Raised by review_coverage when an asserted file is not present under
  // the producer's repoRoot.
  "coverage_file_missing",
  // Raised by review_coverage when an asserted file escapes repoRoot via
  // path traversal (".." or absolute path). Distinct from `coverage_file_missing`
  // so security-relevant signal isn't lumped in with benign typos.
  "coverage_file_escaped",
  // Raised by update_finding_status when the supplied status string is
  // not in the allowed set (e.g. typo, made-up status).
  "invalid_status",
  // Raised when a request payload is structurally invalid (missing required
  // field, self-referential id, malformed dedup request, etc.). Distinct
  // from `finding_not_found` so the agent can tell "ask doesn't exist" from
  // "ask is malformed".
  "invalid_request",
] as const;
export type GateErrorClass = (typeof GATE_ERROR_CLASSES)[number];

// Executor-level error classes (Error{error_class: ...} on the executor
// protocol terminal; consumed by template error_types: keys).
export const EXECUTOR_ERROR_CLASSES = [
  "silence_timeout",
  "tool_error",
  "commit_failed",
  "tests_failed",
] as const;
export type ExecutorErrorClass = (typeof EXECUTOR_ERROR_CLASSES)[number];

export interface GateErrorEnvelope {
  code: number; // always -32000 (MCP application error)
  message: string;
  data: {
    crimefinder_error_class: GateErrorClass;
    retryable: boolean;
    [k: string]: unknown;
  };
}

export function makeGateError(
  cls: GateErrorClass,
  message: string,
  retryable: boolean,
  extras: Record<string, unknown> = {},
): GateErrorEnvelope {
  return {
    code: -32000,
    message,
    data: {
      crimefinder_error_class: cls,
      retryable,
      ...extras,
    },
  };
}

// A thrown error wrapping a GateErrorEnvelope — gate handlers throw this;
// the MCP server catches and returns the envelope to Claude CLI.
export class GateError extends Error {
  readonly envelope: GateErrorEnvelope;
  constructor(envelope: GateErrorEnvelope) {
    super(envelope.message);
    this.envelope = envelope;
    this.name = "GateError";
  }
}
