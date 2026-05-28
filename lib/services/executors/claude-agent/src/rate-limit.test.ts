// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect } from "vitest";
import { detectRateLimit } from "./rate-limit.js";

describe("detectRateLimit", () => {
  it("returns detected=false for empty stderr", () => {
    expect(detectRateLimit("")).toEqual({ detected: false, resumeAt: null, reason: "" });
  });

  it("detects rate_limit_error JSON envelope", () => {
    const r = detectRateLimit(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`);
    expect(r.detected).toBe(true);
    expect(r.reason).toBe("rate_limit");
  });

  it("detects HTTP 429", () => {
    const r = detectRateLimit("upstream returned 429 — backoff requested");
    expect(r.detected).toBe(true);
  });

  it("parses retry-after seconds", () => {
    const now = new Date("2026-05-08T12:00:00Z");
    const r = detectRateLimit("rate limit\nretry-after: 60", now);
    expect(r.detected).toBe(true);
    expect(r.resumeAt?.toISOString()).toBe("2026-05-08T12:01:00.000Z");
  });

  it("parses anthropic-ratelimit-reset epoch", () => {
    const now = new Date("2026-05-08T12:00:00Z");
    const future = Math.floor(now.getTime() / 1000) + 300;
    const r = detectRateLimit(`rate_limit_error\nanthropic-ratelimit-reset: ${future}`, now);
    expect(r.detected).toBe(true);
    expect(r.resumeAt?.toISOString()).toBe("2026-05-08T12:05:00.000Z");
  });

  it("returns null resumeAt when no reset signal present", () => {
    const r = detectRateLimit("rate limit but nothing else");
    expect(r.detected).toBe(true);
    expect(r.resumeAt).toBeNull();
  });

  it("ignores past anthropic-ratelimit-reset epoch", () => {
    const now = new Date("2026-05-08T12:00:00Z");
    const past = Math.floor(now.getTime() / 1000) - 300;
    const r = detectRateLimit(`rate_limit_error\nanthropic-ratelimit-reset: ${past}`, now);
    expect(r.detected).toBe(true);
    expect(r.resumeAt).toBeNull();
  });
});
