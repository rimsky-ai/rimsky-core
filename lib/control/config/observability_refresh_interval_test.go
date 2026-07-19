// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"testing"
	"time"
)

func TestObservabilityRefreshInterval_DefaultsTo60s(t *testing.T) {
	t.Setenv("RIMSKY_OBSERVABILITY_REFRESH_INTERVAL", "")
	if got := ObservabilityRefreshInterval(); got != 60*time.Second {
		t.Fatalf("ObservabilityRefreshInterval() = %s, want 60s (production default)", got)
	}
}

func TestObservabilityRefreshInterval_EnvOverride(t *testing.T) {
	t.Setenv("RIMSKY_OBSERVABILITY_REFRESH_INTERVAL", "5s")
	if got := ObservabilityRefreshInterval(); got != 5*time.Second {
		t.Fatalf("ObservabilityRefreshInterval() = %s, want 5s override", got)
	}
}

func TestObservabilityRefreshInterval_InvalidOrNonPositiveFallsBackToDefault(t *testing.T) {
	for _, v := range []string{"not-a-duration", "0s", "-1s"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("RIMSKY_OBSERVABILITY_REFRESH_INTERVAL", v)
			if got := ObservabilityRefreshInterval(); got != 60*time.Second {
				t.Fatalf("ObservabilityRefreshInterval() with env=%q = %s, want 60s fallback", v, got)
			}
		})
	}
}
