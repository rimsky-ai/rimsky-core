// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

/**
 * Maps a failed agent subprocess's stderr/exit to the precise hierarchical
 * error_class leaf the claude-agent executor advertises in its
 * `declared_error_classes` list (`error-classify.ts`).
 *
 * The classifier is the emit side of the four declared agent failure classes
 * (S-executors-claude-agent-error-classes /
 * 2026-05-23-signal-taxonomy-and-policy-decoupling-design § claude-agent
 * vocabulary). Before this existed, a subprocess that died non-zero with a
 * context-exceeded / refusal / tool-use-failure stderr collapsed into the
 * generic `agent/subprocess_exit/before_complete` leaf, so a subscriber
 * wildcard-keyed on `agent/tool_use_failed/*` or exact-keyed on
 * `agent/context_exceeded` could never fire.
 *
 * Classification is stderr-grep, the same precedent the rate-limit detector
 * (`rate-limit.ts::detectRateLimit`) established for stderr-signature
 * classification — the Claude CLI surfaces these conditions only as free-form
 * stderr lines, not a structured channel. The patterns are matched in a fixed
 * precedence so a stderr line that happens to mention several conditions
 * resolves to a single deterministic leaf:
 *
 *   1. context-window-exceeded → `agent/context_exceeded`
 *   2. tool-invocation failure → `agent/tool_use_failed/<tool>` (the offending
 *      tool name rides the hierarchical leaf; falls back to `unknown` when the
 *      tool cannot be parsed)
 *   3. model refusal           → `agent/refused`
 *
 * Rate-limit classification is intentionally NOT here: a detected rate-limit is
 * routed by the caller — when `cli.handle_rate_limits` is true (the default) it
 * auto-parks via `rate-limit.ts`; only when explicitly false does the caller
 * emit `agent/rate_limited` as an Error class. Keeping that policy decision at
 * the call site (rather than folding it into the classifier) is what lets the
 * default auto-park behavior stay intact while a `handle_rate_limits=false`
 * dispatch surfaces the rate-limit as a terminal Error.
 *
 * Returns `null` when no recognized signature is present, so the caller can
 * fall back to the generic `agent/subprocess_exit/before_complete` leaf
 * (unchanged behavior for an unrecognized non-zero exit).
 */

/** A recognized subprocess failure class. `null` → no signature matched. */
export type ClassifiedAgentError = { errorClass: string } | null;

/**
 * Classify a failed agent subprocess from its accumulated stderr.
 *
 * @param stderr   the subprocess's accumulated stderr text (one or more lines)
 * @param exitCode the subprocess exit code (informational; classification is
 *                 stderr-driven, but kept in the signature so future structured
 *                 channels can refine on exit codes without a call-site change)
 */
export function classifyAgentError(
  stderr: string,
  exitCode: number | null,
): ClassifiedAgentError {
  void exitCode; // @deliberate: currently stderr-driven; see docstring
  if (!stderr) return null;
  const lower = stderr.toLowerCase();

  // @deliberate: 1. Context-window exceeded. The CLI surfaces this as a "prompt is too
  //    long" / "maximum context window" / "context_length_exceeded" /
  //    "context window exceeded" stderr line.
  if (
    lower.includes("context_length_exceeded") ||
    lower.includes("context window") ||
    lower.includes("context_window") ||
    lower.includes("maximum context") ||
    lower.includes("prompt is too long")
  ) {
    return { errorClass: "agent/context_exceeded" };
  }

  // @deliberate: 2. Tool-invocation failure. The offending tool name rides the
  //    hierarchical leaf so a subscriber wildcard-keyed on
  //    `agent/tool_use_failed/*` matches and a policy can pivot on the tool.
  if (lower.includes("tool_use_failed") || lower.includes("tool execution failed")) {
    const tool = parseToolName(stderr) ?? "unknown";
    return { errorClass: `agent/tool_use_failed/${tool}` };
  }

  // @deliberate: 3. Model refusal. A `(refusal)` marker, or the model "declined" / "refused"
  //    to respond.
  if (
    lower.includes("(refusal)") ||
    lower.includes("refused by the model") ||
    lower.includes("declined to respond") ||
    /\brefusal\b/.test(lower)
  ) {
    return { errorClass: "agent/refused" };
  }

  return null;
}

/**
 * Extract the offending tool name from a tool-use-failure stderr line. The CLI
 * reports the tool in a few shapes; we try the most specific first:
 *   - `tool "Bash" returned ...`         (quoted)
 *   - `tool Bash returned ...`           (bare)
 *   - `tool_use_failed: Bash`            (colon-delimited)
 * Returns null when no tool name can be parsed (caller substitutes `unknown`).
 */
function parseToolName(stderr: string): string | null {
  const quoted = /tool\s+["'`]([^"'`]+)["'`]/i.exec(stderr);
  if (quoted && quoted[1]!.length > 0) return quoted[1]!;

  const colon = /tool_use_failed[:\s]+["'`]?([A-Za-z0-9_./-]+)["'`]?/i.exec(stderr);
  if (colon && colon[1]!.length > 0 && colon[1]!.toLowerCase() !== "tool") {
    return colon[1]!;
  }

  const bare = /\btool\s+([A-Za-z0-9_./-]+)\s+(?:returned|failed|errored)/i.exec(stderr);
  if (bare && bare[1]!.length > 0) return bare[1]!;

  return null;
}
