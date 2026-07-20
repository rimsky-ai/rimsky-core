// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package config

import (
	"testing"
	"time"
)

func TestParseObservabilityRefreshInterval_DefaultsTo60s(t *testing.T) {
	t.Setenv("RIMSKY_OBSERVABILITY_REFRESH_INTERVAL", "")
	got, err := parseObservabilityRefreshInterval()
	if err != nil {
		t.Fatalf("parseObservabilityRefreshInterval() error = %v", err)
	}
	if got != 60*time.Second {
		t.Fatalf("parseObservabilityRefreshInterval() = %s, want 60s (production default)", got)
	}
}

func TestParseObservabilityRefreshInterval_EnvOverride(t *testing.T) {
	t.Setenv("RIMSKY_OBSERVABILITY_REFRESH_INTERVAL", "5s")
	got, err := parseObservabilityRefreshInterval()
	if err != nil {
		t.Fatalf("parseObservabilityRefreshInterval() error = %v", err)
	}
	if got != 5*time.Second {
		t.Fatalf("parseObservabilityRefreshInterval() = %s, want 5s override", got)
	}
}

func TestParseObservabilityRefreshInterval_InvalidOrNonPositiveFailsLoud(t *testing.T) {
	for _, v := range []string{"not-a-duration", "0s", "-1s"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("RIMSKY_OBSERVABILITY_REFRESH_INTERVAL", v)
			if _, err := parseObservabilityRefreshInterval(); err == nil {
				t.Fatalf("parseObservabilityRefreshInterval() with env=%q: want error, got nil (malformed/non-positive values must not be silently dropped)", v)
			}
		})
	}
}
