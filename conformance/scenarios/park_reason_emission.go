// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package scenarios

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguyconsulting/rimsky/conformance"
	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

func init() {
	conformance.Register(conformance.Scenario{
		Name:         "park_reason_emission",
		RequiresStub: true,
		Run:          runParkReasonEmission,
	})
}

// runParkReasonEmission probes the executor's Park.reason emission
// shape. Drives stub mode with `probe_park: true, park_reason:
// "await_callback"`; asserts the StreamClose.Park.reason field
// round-trips a value in the closed two-value set
// (proto:executor.proto::ParkReason — AWAIT_CALLBACK | SNOOZE). The
// proto wire layer caps the set at decode, so this probe pins the
// executor's emission shape rather than re-enforcing the enum cap.
//
// Per spec .ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md
// §ParkReason collapse.
func runParkReasonEmission(ctx context.Context, env conformance.Env) error {
	ud, _ := structpb.NewStruct(map[string]any{
		"probe_park":       true,
		"park_reason":      "await_callback",
		"park_reason_note": "rimsky-executor-conformance park-reason probe",
	})
	req := &genv1.ExecuteRequest{
		NodeId: "conformance", InstanceId: "conformance",
		NodeType: "conformance-probe", Attributes: ud,
		CallbackUrl: env.Callbacks.URL(),
	}
	stream, err := env.Client.Execute(ctx, req)
	if err != nil {
		return fmt.Errorf("execute: %w", err)
	}
	defer stream.Close()

	ev, err := conformance.AwaitTerminal(ctx, stream, env)
	if err != nil {
		return err
	}
	sc, ok := ev.Event.(*genv1.ExecuteEvent_StreamClose)
	if !ok {
		return fmt.Errorf("unexpected terminal type: %T", ev.Event)
	}
	park, ok := sc.StreamClose.Outcome.(*genv1.StreamClose_Park)
	if !ok {
		return fmt.Errorf("expected Park outcome, got %T", sc.StreamClose.Outcome)
	}
	r := park.Park.GetReason()
	if r != genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK && r != genv1.ParkReason_PARK_REASON_SNOOZE {
		return fmt.Errorf("Park.reason=%v is outside the closed two-value set (AWAIT_CALLBACK | SNOOZE)", r)
	}
	return nil
}
