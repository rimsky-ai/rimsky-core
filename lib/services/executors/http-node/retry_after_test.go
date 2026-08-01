// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package httpnode

import (
	"net/http"
	"testing"
	"time"
)

func fixedNow(now time.Time) func() time.Time {
	return func() time.Time { return now }
}

func TestParseRetryAfter_IntegerSeconds(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := parseRetryAfter("7", fixedNow(now))
	want := now.Add(7 * time.Second)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseRetryAfter_NegativeSecondsFallsBackToDefault(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := parseRetryAfter("-5", fixedNow(now))
	want := now.Add(defaultRetryAfter)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v (negative seconds must fall back to the default)", got, want)
	}
}

func TestParseRetryAfter_EmptyHeaderUsesDefault(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := parseRetryAfter("", fixedNow(now))
	want := now.Add(defaultRetryAfter)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseRetryAfter_MalformedHeaderFallsBackToDefault(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := parseRetryAfter("not-a-valid-retry-after-value", fixedNow(now))
	want := now.Add(defaultRetryAfter)
	if !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseRetryAfter_HTTPDateFuture(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	future := now.Add(2 * time.Hour)
	header := future.UTC().Format(http.TimeFormat)
	got := parseRetryAfter(header, fixedNow(now))
	if !got.Equal(future) {
		t.Fatalf("got %v, want %v (parsed from %q)", got, future, header)
	}
}

func TestParseRetryAfter_PastDateClampedToNow(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	past := now.Add(-2 * time.Hour)
	header := past.UTC().Format(http.TimeFormat)
	got := parseRetryAfter(header, fixedNow(now))
	if !got.Equal(now) {
		t.Fatalf("got %v, want %v (a past HTTP-date must clamp to now, not extend the park backward)", got, now)
	}
}

func TestParseRetryAfter_RFC1123Format(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	future := now.Add(30 * time.Minute)
	header := future.UTC().Format(time.RFC1123)
	got := parseRetryAfter(header, fixedNow(now))
	if !got.Equal(future) {
		t.Fatalf("got %v, want %v (parsed from RFC1123 %q)", got, future, header)
	}
}
