// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"fmt"
	"testing"
	"time"
)

func TestDetectRateLimitEmptyStderr(t *testing.T) {
	r := DetectRateLimit("", time.Now())
	if r.Detected || r.ResumeAt != nil || r.Reason != "" {
		t.Fatalf("expected zero signal, got %+v", r)
	}
}

func TestDetectRateLimitErrorEnvelope(t *testing.T) {
	r := DetectRateLimit(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`, time.Now())
	if !r.Detected || r.Reason != "rate_limit" {
		t.Fatalf("expected detection, got %+v", r)
	}
}

func TestDetectRateLimitHTTP429(t *testing.T) {
	r := DetectRateLimit("upstream returned 429 — backoff requested", time.Now())
	if !r.Detected {
		t.Fatalf("expected detection, got %+v", r)
	}
}

func TestDetectRateLimitParsesRetryAfterSeconds(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	r := DetectRateLimit("rate limit\nretry-after: 60", now)
	if !r.Detected || r.ResumeAt == nil {
		t.Fatalf("expected resumeAt, got %+v", r)
	}
	want := time.Date(2026, 5, 8, 12, 1, 0, 0, time.UTC)
	if !r.ResumeAt.Equal(want) {
		t.Fatalf("resumeAt = %v, want %v", r.ResumeAt, want)
	}
}

func TestDetectRateLimitParsesAnthropicResetEpoch(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	future := now.Unix() + 300
	r := DetectRateLimit(fmt.Sprintf("rate_limit_error\nanthropic-ratelimit-reset: %d", future), now)
	if !r.Detected || r.ResumeAt == nil {
		t.Fatalf("expected resumeAt, got %+v", r)
	}
	want := time.Date(2026, 5, 8, 12, 5, 0, 0, time.UTC)
	if !r.ResumeAt.Equal(want) {
		t.Fatalf("resumeAt = %v, want %v", r.ResumeAt, want)
	}
}

func TestDetectRateLimitNoResetSignal(t *testing.T) {
	r := DetectRateLimit("rate limit but nothing else", time.Now())
	if !r.Detected || r.ResumeAt != nil {
		t.Fatalf("expected nil resumeAt, got %+v", r)
	}
}

func TestDetectRateLimitIgnoresPastResetEpoch(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	past := now.Unix() - 300
	r := DetectRateLimit(fmt.Sprintf("rate_limit_error\nanthropic-ratelimit-reset: %d", past), now)
	if !r.Detected || r.ResumeAt != nil {
		t.Fatalf("expected nil resumeAt for past epoch, got %+v", r)
	}
}

func TestDetectRateLimitParsesISOReset(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	r := DetectRateLimit("rate limit\nresetAt: 2026-05-08T12:30:00Z", now)
	if !r.Detected || r.ResumeAt == nil {
		t.Fatalf("expected resumeAt, got %+v", r)
	}
	want := time.Date(2026, 5, 8, 12, 30, 0, 0, time.UTC)
	if !r.ResumeAt.Equal(want) {
		t.Fatalf("resumeAt = %v, want %v", r.ResumeAt, want)
	}
}
