// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package agentport

import "testing"

func TestResolvePrefersAgentPortOverFallbackVar(t *testing.T) {
	t.Setenv(EnvVar, "5555")
	t.Setenv("SOME_SERVICE_PORT", "9999")

	if got := Resolve("SOME_SERVICE_PORT", 1234); got != 5555 {
		t.Fatalf("Resolve = %d, want 5555 (RIMSKY_AGENT_PORT should win)", got)
	}
}

func TestResolveFallsBackToServiceVarWhenAgentPortUnset(t *testing.T) {
	t.Setenv("SOME_SERVICE_PORT", "9999")

	if got := Resolve("SOME_SERVICE_PORT", 1234); got != 9999 {
		t.Fatalf("Resolve = %d, want 9999 (fallback var)", got)
	}
}

func TestResolveFallsBackToDefaultWhenNeitherSet(t *testing.T) {
	if got := Resolve("SOME_SERVICE_PORT", 1234); got != 1234 {
		t.Fatalf("Resolve = %d, want 1234 (default)", got)
	}
}

func TestResolveIgnoresMalformedAgentPort(t *testing.T) {
	t.Setenv(EnvVar, "not-a-number")
	t.Setenv("SOME_SERVICE_PORT", "9999")

	if got := Resolve("SOME_SERVICE_PORT", 1234); got != 9999 {
		t.Fatalf("Resolve = %d, want 9999 (malformed RIMSKY_AGENT_PORT falls through)", got)
	}
}

func TestOverridePrefersAgentPort(t *testing.T) {
	t.Setenv(EnvVar, "5555")

	if got := Override(1234); got != 5555 {
		t.Fatalf("Override = %d, want 5555", got)
	}
}

func TestOverrideFallsBackWhenUnset(t *testing.T) {
	if got := Override(1234); got != 1234 {
		t.Fatalf("Override = %d, want 1234", got)
	}
}
