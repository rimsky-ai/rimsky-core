// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

/**
 * Detection helpers for Anthropic API rate-limit events (`rate-limit.ts`).
 *
 * Per the 2026-05-08 platform-extensions plan J9, claude-agent should
 * convert detected rate limits into a `Park` terminal so the rimsky
 * supervisor parks the node and resumes after the reset window.
 *
 * The CLI surfaces rate limits via stderr lines containing
 * `rate_limit_error` or "429" or the literal "rate limit". Reset
 * timestamps appear in the response headers / message body as
 * `retry-after` (seconds-until-reset) or as an absolute Unix epoch in
 * `anthropic-ratelimit-reset`. The detector below is line-grep + a few
 * patterns; future cli versions may expose a structured channel.
 */

/** Result of parsing CLI stderr for a rate-limit signal. */
export interface RateLimitSignal {
  detected: boolean;
  resumeAt: Date | null;
  /** Recommended display reason for the `Park` terminal. */
  reason: string;
}

/**
 * Detect rate-limit signals in stderr text (one or more lines). Returns
 * detected=false when no rate-limit pattern matches.
 *
 * Patterns recognized:
 *   - "rate_limit_error"          (Claude API JSON envelope)
 *   - "rate limit" (case-insens.) (free-form CLI error)
 *   - " 429 "                     (HTTP status)
 *   - "retry-after: <seconds>"    (RFC 7231 header)
 *   - "anthropic-ratelimit-reset: <unix-epoch>"
 *   - "anthropic-ratelimit-tokens-reset: <unix-epoch>"
 *   - "ResetAt: 2026-05-08T..."   (Anthropic SDK structured field)
 */
export function detectRateLimit(stderr: string, now: Date = new Date()): RateLimitSignal {
  if (!stderr) return { detected: false, resumeAt: null, reason: "" };
  const lower = stderr.toLowerCase();
  const detected =
    lower.includes("rate_limit_error") ||
    lower.includes("rate limit") ||
    /\b429\b/.test(lower);
  if (!detected) {
    return { detected: false, resumeAt: null, reason: "" };
  }
  const resumeAt = parseResumeAt(stderr, now);
  return {
    detected: true,
    resumeAt,
    reason: "rate_limit",
  };
}

/**
 * Parse a wall-clock resume time out of stderr. Returns null when no
 * reset signal was found.
 */
function parseResumeAt(stderr: string, now: Date): Date | null {
  // @deliberate: 1. retry-after: <seconds>
  const retryAfter = /retry-after:\s*(\d+)/i.exec(stderr);
  if (retryAfter) {
    const seconds = parseInt(retryAfter[1], 10);
    if (!isNaN(seconds) && seconds > 0) {
      return new Date(now.getTime() + seconds * 1000);
    }
  }
  // @deliberate: 2. anthropic-ratelimit-(tokens-)reset: <unix-epoch>
  const epochReset = /anthropic-ratelimit(?:-tokens)?-reset:\s*(\d+)/i.exec(stderr);
  if (epochReset) {
    const epoch = parseInt(epochReset[1], 10);
    if (!isNaN(epoch) && epoch > now.getTime() / 1000) {
      return new Date(epoch * 1000);
    }
  }
  // @deliberate: 3. ResetAt: 2026-05-08T...Z (RFC3339)
  const isoReset = /resetat[:= ]\s*([0-9T:\-+.Z]+)/i.exec(stderr);
  if (isoReset) {
    const t = new Date(isoReset[1]);
    if (!isNaN(t.getTime()) && t.getTime() > now.getTime()) {
      return t;
    }
  }
  return null;
}
