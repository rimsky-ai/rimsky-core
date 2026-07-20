// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package agentport

import "testing"

func TestResolvePrefersAgentPortOverFallbackVar(t *testing.T) {
	t.Setenv(EnvVar, "5555")
	t.Setenv("SOME_SERVICE_PORT", "9999")

	got, err := Resolve("SOME_SERVICE_PORT", 1234)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != 5555 {
		t.Fatalf("Resolve = %d, want 5555 (RIMSKY_AGENT_PORT should win)", got)
	}
}

func TestResolveFallsBackToServiceVarWhenAgentPortUnset(t *testing.T) {
	t.Setenv("SOME_SERVICE_PORT", "9999")

	got, err := Resolve("SOME_SERVICE_PORT", 1234)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != 9999 {
		t.Fatalf("Resolve = %d, want 9999 (fallback var)", got)
	}
}

func TestResolveFallsBackToDefaultWhenNeitherSet(t *testing.T) {
	got, err := Resolve("SOME_SERVICE_PORT", 1234)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != 1234 {
		t.Fatalf("Resolve = %d, want 1234 (default)", got)
	}
}

func TestResolveFailsClosedOnMalformedAgentPort(t *testing.T) {
	t.Setenv(EnvVar, "not-a-number")
	t.Setenv("SOME_SERVICE_PORT", "9999")

	if _, err := Resolve("SOME_SERVICE_PORT", 1234); err == nil {
		t.Fatal("Resolve: expected an error for a malformed RIMSKY_AGENT_PORT, got nil " +
			"(silently falling through to another port would let a typo bind the wrong port)")
	}
}

func TestResolveFailsClosedOnMalformedFallbackVar(t *testing.T) {
	t.Setenv("SOME_SERVICE_PORT", "not-a-number")

	if _, err := Resolve("SOME_SERVICE_PORT", 1234); err == nil {
		t.Fatal("Resolve: expected an error for a malformed fallback port var, got nil")
	}
}

func TestOverridePrefersAgentPort(t *testing.T) {
	t.Setenv(EnvVar, "5555")

	got, err := Override(1234)
	if err != nil {
		t.Fatalf("Override: %v", err)
	}
	if got != 5555 {
		t.Fatalf("Override = %d, want 5555", got)
	}
}

func TestOverrideFallsBackWhenUnset(t *testing.T) {
	got, err := Override(1234)
	if err != nil {
		t.Fatalf("Override: %v", err)
	}
	if got != 1234 {
		t.Fatalf("Override = %d, want 1234", got)
	}
}

func TestOverrideFailsClosedOnMalformedAgentPort(t *testing.T) {
	t.Setenv(EnvVar, "not-a-number")

	if _, err := Override(1234); err == nil {
		t.Fatal("Override: expected an error for a malformed RIMSKY_AGENT_PORT, got nil")
	}
}
