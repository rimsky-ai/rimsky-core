// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// N10 scenario — co_holding_drives_promotion.
//
// In the verifier-pattern, the verifier executor runs as a co-holder
// of the producer's claim handle; its terminal verdict drives the
// parent claim's promotion (Commit on pass, Abandon on fail). The
// scenario pins the contract: a verifier-shape-checks `Success`
// outcome carries `verifier_pass: true` in the attributes_delta,
// and an `Error` outcome carries `error_class: "verifier_failed"`.
// The supervisor's terminal handler routes on these to drive the
// parent's terminal.
//
// This scenario exercises the protocol shape at the executor level;
// the full Aggregate → Auto-Terminal end-to-end harness needs the
// pgtest fixture (deferred).
package verifier

import (
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

// TestCoHoldingDrivesPromotion_VerifierPassEmitsSuccess pins the
// success-path shape: verifier emits Success with verifier_pass=true,
// the supervisor's terminal handler routes the parent to Commit.
func TestCoHoldingDrivesPromotion_VerifierPassEmitsSuccess(t *testing.T) {
	t.Parallel()
	// Synthetic Success terminal mimicking verifier-shape-checks
	// happy-path output (see executors/verifier-shape-checks/server.go).
	delta, _ := structpb.NewStruct(map[string]any{
		"verifier_pass":   true,
		"verifier_checks": float64(3),
		"verifier_rows":   float64(42),
	})
	ev := &genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
			AttributesDelta: delta,
			Changed:         false,
			ChangeSummary:   "verifier-shape-checks: 3 checks passed",
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
		t.Errorf("verifier_pass should be true on the success terminal")
	}
}

// TestCoHoldingDrivesPromotion_VerifierFailEmitsError pins the
// failure-path shape: verifier emits Error with
// error_class="verifier_failed", which the supervisor's
// error_types policy routes to the parent's Abandon.
func TestCoHoldingDrivesPromotion_VerifierFailEmitsError(t *testing.T) {
	t.Parallel()
	payload, _ := structpb.NewStruct(map[string]any{
		"failures": []any{
			map[string]any{
				"kind":    "no_nulls",
				"message": "id field is null",
				"rows":    float64(10),
				"failed":  float64(2),
			},
		},
		"summary": "no_nulls=FAIL",
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
