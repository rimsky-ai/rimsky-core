// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/persistence"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
)

// @decision: three-dispatch-deadlines
func TestDecideExecutorDeadlineRelease_MaxRuntimeExceeded(t *testing.T) {
	now := time.Now()
	claimedAt := now.Add(-2 * time.Hour)
	maxRuntimeSeconds := 3600
	o := persistence.DispatchRow{
		ID:                         shared.UUID(uuid.New()),
		ClaimedAt:                  &claimedAt,
		EffectiveMaxRuntimeSeconds: &maxRuntimeSeconds,
	}
	errorClass, _, reason := decideExecutorDeadlineRelease(o, now)
	if errorClass != "max_runtime_exceeded" {
		t.Fatalf("expected errorClass=max_runtime_exceeded, got %q (reason=%q)", errorClass, reason)
	}
	if reason == "" {
		t.Fatalf("expected a non-empty release reason for max_runtime_exceeded")
	}
}

func TestDecideExecutorDeadlineRelease_MaxRuntimeNotExceeded(t *testing.T) {
	now := time.Now()
	claimedAt := now.Add(-30 * time.Minute)
	maxRuntimeSeconds := 3600
	o := persistence.DispatchRow{
		ID:                         shared.UUID(uuid.New()),
		ClaimedAt:                  &claimedAt,
		EffectiveMaxRuntimeSeconds: &maxRuntimeSeconds,
	}
	errorClass, _, _ := decideExecutorDeadlineRelease(o, now)
	if errorClass != "" {
		t.Fatalf("expected no release below the max_runtime deadline, got errorClass=%q", errorClass)
	}
}

func TestDecideExecutorDeadlineRelease_MaxRuntimeTakesPrecedenceOverQuietPeriod(t *testing.T) {
	now := time.Now()
	claimedAt := now.Add(-2 * time.Hour)
	lastProgress := now.Add(-1 * time.Minute)
	maxRuntimeSeconds := 3600
	maxQuietPeriodSeconds := 999999
	o := persistence.DispatchRow{
		ID:                             shared.UUID(uuid.New()),
		ClaimedAt:                      &claimedAt,
		LastProgressAt:                 &lastProgress,
		EffectiveMaxRuntimeSeconds:     &maxRuntimeSeconds,
		EffectiveMaxQuietPeriodSeconds: &maxQuietPeriodSeconds,
	}
	errorClass, _, _ := decideExecutorDeadlineRelease(o, now)
	if errorClass != "max_runtime_exceeded" {
		t.Fatalf("expected max_runtime_exceeded to fire even with a fresh quiet-period signal, got %q", errorClass)
	}
}

// @decision: three-dispatch-deadlines
func TestDecideExecutorDeadlineRelease_MaxRuntimeWinsWhenBothDeadlinesActuallyBreached(t *testing.T) {
	now := time.Now()
	claimedAt := now.Add(-2 * time.Hour)
	lastProgress := now.Add(-90 * time.Minute)
	maxRuntimeSeconds := 3600
	maxQuietPeriodSeconds := 60
	o := persistence.DispatchRow{
		ID:                             shared.UUID(uuid.New()),
		ClaimedAt:                      &claimedAt,
		LastProgressAt:                 &lastProgress,
		EffectiveMaxRuntimeSeconds:     &maxRuntimeSeconds,
		EffectiveMaxQuietPeriodSeconds: &maxQuietPeriodSeconds,
	}
	errorClass, _, _ := decideExecutorDeadlineRelease(o, now)
	if errorClass != "max_runtime_exceeded" {
		t.Fatalf("both deadlines are genuinely breached here (runtime 2h > 1h max_runtime, AND quiet "+
			"90m > 60s max_quiet_period) — max_runtime must still win the tiebreak, got errorClass=%q",
			errorClass)
	}
}
