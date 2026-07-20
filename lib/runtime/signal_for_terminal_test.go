// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package runtime

import (
	"testing"

	signalpkg "github.com/rimsky-ai/rimsky-core/lib/foundation/signal"
	"github.com/rimsky-ai/rimsky-core/lib/foundation/spec"
)

func TestSignalForTerminal_ErroredReusesThePolicyEmittedSignal(t *testing.T) {
	policySig := signalpkg.BuildTerminalErrorSignal("infra/dial_failed", map[string]any{"reason": "boom"}, 3, 3, nil, nil)
	acq := &acquisition{
		RetryDecision: &policyDecision{Kind: spec.ActionGiveUp, Signal: policySig},
	}
	t1 := terminalEvent{
		Kind:       terminalKindErrored,
		ErrorClass: "infra/dial_failed",
		Payload:    map[string]any{"error": "boom"},
	}

	got := signalForTerminal(RunArgs{}, acq, t1)
	if got.Type != policySig.Type {
		t.Fatalf("Type = %q, want the policy-emitted %q", got.Type, policySig.Type)
	}
	if got.Payload["attempt"] != policySig.Payload["attempt"] {
		t.Fatalf("Payload[attempt] = %v, want %v (the real retry count, not hardcoded 0)",
			got.Payload["attempt"], policySig.Payload["attempt"])
	}
}

func TestSignalForTerminal_InfraReusesThePolicyEmittedSignal(t *testing.T) {
	policySig := signalpkg.BuildTerminalErrorSignal("executor_dial_failed", map[string]any{"error": "conn refused"}, 2, 2, nil, nil)
	acq := &acquisition{
		RetryDecision: &policyDecision{Kind: spec.ActionGiveUp, Signal: policySig},
	}
	t1 := terminalEvent{
		Kind:       terminalKindInfra,
		ErrorClass: "executor_dial_failed",
		Payload:    map[string]any{"error": "conn refused"},
	}

	got := signalForTerminal(RunArgs{}, acq, t1)
	if got.Type != policySig.Type {
		t.Fatalf("Type = %q, want the policy-emitted %q", got.Type, policySig.Type)
	}
}

func TestSignalForTerminal_ErroredFallsBackWithoutRetryDecision(t *testing.T) {
	t1 := terminalEvent{
		Kind:       terminalKindErrored,
		ErrorClass: "some_class",
		Payload:    map[string]any{"payload": map[string]any{"k": "v"}},
	}

	got := signalForTerminal(RunArgs{}, nil, t1)
	if got.Payload["error_payload"] == nil {
		t.Fatalf("expected the fallback reconstruction to still extract error_payload, got %+v", got.Payload)
	}
}
