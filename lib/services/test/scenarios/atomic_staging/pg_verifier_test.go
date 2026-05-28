// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N9 scenario — postgres-store fused verifier role.
//
// Validates the wire-shape end of the atomic-staging-with-postgres-
// verifier pattern: a verifier terminal MUST emit a Success outcome on
// all-checks-pass and an Error with error_class="verifier_failed" on
// any-fail, matching the contract that the supervisor's terminal
// handler routes on. The substrate-side SQL execution is exercised by
// the executor unit tests at
// stores/postgres/server/executor_test.go (testcontainers-driven), so
// this scenario pins the terminal-event shape that the rest of the
// rimsky core depends on without re-booting a container per test.
//
// Per spec
// .ok-planner/specs/2026-05-19-multi-instance-template-ergonomics-design.md
// §Item 6.
package atomicstaging

import (
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// TestPGVerifier_SuccessShape mirrors the terminal-event the fused
// `stores/postgres/` verifier role emits on all-checks-pass. The
// supervisor's terminal handler routes a Success outcome from a
// co-holding verifier into the parent claim's Commit (atomic schema
// swap).
func TestPGVerifier_SuccessShape(t *testing.T) {
	t.Parallel()
	delta, _ := structpb.NewStruct(map[string]any{
		"verifier_pass":   true,
		"verifier_checks": float64(3),
		"results": []any{
			map[string]any{"kind": "no_nulls", "pass": true},
			map[string]any{"kind": "row_count_absolute", "pass": true},
			map[string]any{"kind": "pk_unique", "pass": true},
		},
	})
	ev := &genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
			AttributesDelta: delta,
			Changed:         false,
			ChangeSummary:   "postgres-store verifier: 3 checks passed on staging_x.items",
		}}},
	}}
	sc := ev.GetStreamClose()
	if sc == nil {
		t.Fatal("expected StreamClose event")
	}
	success := sc.GetSuccess()
	if success == nil {
		t.Fatal("expected Success outcome")
	}
	if success.GetAttributesDelta().GetFields()["verifier_pass"].GetBoolValue() != true {
		t.Error("verifier_pass should be true on success")
	}
}

// TestPGVerifier_ErrorShape mirrors the terminal-event the fused
// verifier role emits on any-check-fail. The error_class
// "verifier_failed" is the supervisor's routing key into the parent's
// error_types policy (typically resolve: error → Abandon on the parent
// claim).
func TestPGVerifier_ErrorShape(t *testing.T) {
	t.Parallel()
	payload, _ := structpb.NewStruct(map[string]any{
		"failures": []any{
			map[string]any{
				"kind":    "row_count_absolute",
				"message": "row_count_absolute: 999 rows < min 1000",
				"counts":  map[string]any{"row_count": float64(999), "min": float64(1000)},
			},
		},
		"summary": "row_count_absolute=FAIL",
	})
	ev := &genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Error{Error: &genv1.Error{
			ErrorClass: "verifier_failed",
			Payload:    payload,
		}}},
	}}
	sc := ev.GetStreamClose()
	errOutcome := sc.GetError()
	if errOutcome == nil {
		t.Fatal("expected Error outcome")
	}
	if errOutcome.GetErrorClass() != "verifier_failed" {
		t.Errorf("error_class: got %q want verifier_failed", errOutcome.GetErrorClass())
	}
}
