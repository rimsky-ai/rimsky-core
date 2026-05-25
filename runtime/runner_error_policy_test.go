// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"testing"

	signalpkg "github.com/fallguyconsulting/rimsky/foundation/signal"
)

// TestErrorPolicySignal_RetryShape covers the per-action emission
// rules in errorPolicySignal. The end-to-end emit (the full
// applyErrorPolicy → EmitSignal write into rimsky_events) is
// covered in test/scenarios/signal_emission_test.go; this unit-test
// pins the type-path construction so the retire-Pass-5 work and
// future Pass-3 refactors don't silently change the wire shape.
func TestErrorPolicySignal_RetryShape(t *testing.T) {
	got := errorPolicySignal("foo", map[string]any{"k": "v"}, "retry", 1, 500)
	if got.Type != signalpkg.TypePath("transient/retry/1/foo") {
		t.Fatalf("retry type: got %q want transient/retry/1/foo", got.Type)
	}
	if got.Payload["attempt"].(int) != 1 {
		t.Fatalf("attempt: got %v want 1", got.Payload["attempt"])
	}
	if got.Payload["error_class"].(string) != "foo" {
		t.Fatalf("error_class: got %v want foo", got.Payload["error_class"])
	}
	if got.Payload["discarded_claims"].(bool) {
		t.Fatalf("retry: discarded_claims should be false")
	}
	if got.Payload["delay_ms"].(int) != 500 {
		t.Fatalf("delay_ms: got %v want 500", got.Payload["delay_ms"])
	}
}

func TestErrorPolicySignal_DiscardClaimsThenRetryShape(t *testing.T) {
	got := errorPolicySignal("foo", nil, "discard_claims_then_retry", 2, 0)
	if got.Type != signalpkg.TypePath("transient/retry/2/foo") {
		t.Fatalf("type: got %q", got.Type)
	}
	if !got.Payload["discarded_claims"].(bool) {
		t.Fatalf("discard_claims_then_retry: discarded_claims should be true")
	}
}

func TestErrorPolicySignal_GiveUpShape(t *testing.T) {
	got := errorPolicySignal("http/timeout", map[string]any{"status": 504}, "give_up", 0, 0)
	if got.Type != signalpkg.TypePath("terminal/error/http/timeout") {
		t.Fatalf("give_up type: got %q want terminal/error/http/timeout", got.Type)
	}
	if got.Payload["error_class"].(string) != "http/timeout" {
		t.Fatalf("error_class: got %v", got.Payload["error_class"])
	}
}

func TestErrorPolicySignal_PassShape(t *testing.T) {
	got := errorPolicySignal("foo", nil, "pass", 0, 0)
	if got.Type != signalpkg.TypePath("terminal/error/foo") {
		t.Fatalf("pass type: got %q", got.Type)
	}
}
