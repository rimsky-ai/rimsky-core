// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

type RateLimitSignal struct {
	Detected bool
	ResumeAt *time.Time
	Reason   string
}

var (
	http429Re    = regexp.MustCompile(`\b429\b`)
	retryAfterRe = regexp.MustCompile(`(?i)retry-after:\s*(\d+)`)
	epochResetRe = regexp.MustCompile(`(?i)anthropic-ratelimit(?:-tokens|-requests)?-reset:\s*(\d+)`)
	isoResetRe   = regexp.MustCompile(`(?i)resetat[:= ]\s*([0-9T:\-+.Z]+)`)
)

func DetectRateLimit(stderr string, now time.Time) RateLimitSignal {
	if stderr == "" {
		return RateLimitSignal{}
	}
	lower := strings.ToLower(stderr)
	detected := strings.Contains(lower, "rate_limit_error") ||
		strings.Contains(lower, "rate limit") ||
		http429Re.MatchString(lower)
	if !detected {
		return RateLimitSignal{}
	}
	return RateLimitSignal{
		Detected: true,
		ResumeAt: parseResumeAt(stderr, now),
		Reason:   "rate_limit",
	}
}

func parseResumeAt(stderr string, now time.Time) *time.Time {
	if m := retryAfterRe.FindStringSubmatch(stderr); m != nil {
		seconds, err := strconv.Atoi(m[1])
		if err == nil && seconds > 0 {
			t := now.Add(time.Duration(seconds) * time.Second)
			return &t
		}
	}
	if m := epochResetRe.FindStringSubmatch(stderr); m != nil {
		epoch, err := strconv.ParseInt(m[1], 10, 64)
		if err == nil && epoch > now.Unix() {
			t := time.Unix(epoch, 0)
			return &t
		}
	}
	if m := isoResetRe.FindStringSubmatch(stderr); m != nil {
		t, err := time.Parse(time.RFC3339, m[1])
		if err == nil && t.After(now) {
			return &t
		}
	}
	return nil
}
