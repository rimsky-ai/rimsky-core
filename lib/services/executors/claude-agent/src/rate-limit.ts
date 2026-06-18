// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

export interface RateLimitSignal {
  detected: boolean;
  resumeAt: Date | null;
  reason: string;
}

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

function parseResumeAt(stderr: string, now: Date): Date | null {
  const retryAfter = /retry-after:\s*(\d+)/i.exec(stderr);
  if (retryAfter) {
    const seconds = parseInt(retryAfter[1], 10);
    if (!isNaN(seconds) && seconds > 0) {
      return new Date(now.getTime() + seconds * 1000);
    }
  }
  const epochReset = /anthropic-ratelimit(?:-tokens)?-reset:\s*(\d+)/i.exec(stderr);
  if (epochReset) {
    const epoch = parseInt(epochReset[1], 10);
    if (!isNaN(epoch) && epoch > now.getTime() / 1000) {
      return new Date(epoch * 1000);
    }
  }
  const isoReset = /resetat[:= ]\s*([0-9T:\-+.Z]+)/i.exec(stderr);
  if (isoReset) {
    const t = new Date(isoReset[1]);
    if (!isNaN(t.getTime()) && t.getTime() > now.getTime()) {
      return t;
    }
  }
  return null;
}
