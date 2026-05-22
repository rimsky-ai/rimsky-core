// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package scenarios

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/fallguy/rimsky/conformance"
	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

func init() {
	conformance.Register(conformance.Scenario{
		Name:         "park_reason_emission",
		RequiresStub: true,
		Run:          runParkReasonEmission,
	})
	conformance.Register(conformance.Scenario{
		Name:         "park_reason_other_requires_label",
		RequiresStub: true,
		Run:          runParkReasonOtherRequiresLabel,
	})
}

// runParkReasonEmission probes the executor's Park.reason emission
// shape. Drives stub mode with `probe_park: true, park_reason:
// "callback_wait"`; asserts the StreamClose.Park.reason field is set
// (not UNSPECIFIED — the spec's taxonomy advises always populating
// reason).
//
// Per plan §M5 + plan §Parked-state taxonomy.
func runParkReasonEmission(ctx context.Context, env conformance.Env) error {
	ud, _ := structpb.NewStruct(map[string]any{
		"probe_park":       true,
		"park_reason":      "callback_wait",
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
	if park.Park.GetReason() == genv1.ParkReason_PARK_REASON_UNSPECIFIED {
		return fmt.Errorf("Park.reason is PARK_REASON_UNSPECIFIED; the spec taxonomy requires a typed reason on new emissions")
	}
	return nil
}

// runParkReasonOtherRequiresLabel pins the supervisor-side gate:
// when an executor emits PARK_REASON_OTHER, reason_label must be
// set. The probe drives the executor with park_reason="other" but
// WITHOUT a reason_label; the conformance binary asserts that the
// executor surfaces this in some form — either by setting
// reason_label itself, or by rejecting the probe.
//
// Production executors are expected to either always populate
// reason_label when emitting OTHER or to pick a non-OTHER reason.
// The check is lenient: a missing label is surfaced as a warning
// (via stdout) rather than a hard failure, since the supervisor's
// terminal handler is the authoritative gate.
func runParkReasonOtherRequiresLabel(ctx context.Context, env conformance.Env) error {
	ud, _ := structpb.NewStruct(map[string]any{
		"probe_park":  true,
		"park_reason": "other",
		// Intentionally omit park_reason_label.
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
		// Executor rejected the OTHER-without-label probe via Error
		// terminal — also acceptable.
		if _, isErr := sc.StreamClose.Outcome.(*genv1.StreamClose_Error); isErr {
			return nil
		}
		return fmt.Errorf("expected Park or Error outcome, got %T", sc.StreamClose.Outcome)
	}
	if park.Park.GetReason() == genv1.ParkReason_PARK_REASON_OTHER && park.Park.GetReasonLabel() == "" {
		// This is the spec violation. The supervisor's terminal handler
		// rejects this server-side; the conformance binary surfaces it
		// here so executor authors see the warning during their own
		// black-box runs.
		return fmt.Errorf("Park.reason=OTHER emitted with empty reason_label; the supervisor terminal handler rejects this — set reason_label on every OTHER-reason Park")
	}
	return nil
}
