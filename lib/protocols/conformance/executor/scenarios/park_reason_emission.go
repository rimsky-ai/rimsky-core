// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package scenarios

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	conformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @deliberate: park_reason_emission asserts the Park outcome carries
// a typed reason from the closed two-value set
// (PARK_REASON_AWAIT_CALLBACK | PARK_REASON_SNOOZE). Per
// TD-remove-resume-context the Park outcome no longer carries
// `payload` or `session_token` — those proto fields are reserved.
// State that needs to ride across the park boundary lives on
// `attributes_delta`.
//
// @concept: parked-state
func init() {
	conformance.Register(conformance.Scenario{
		Name:         "park_reason_emission",
		RequiresStub: true,
		Run: func(ctx context.Context, env conformance.Env) error {
			attrs, err := structpb.NewStruct(map[string]any{
				"probe_park":  true,
				"park_reason": "await_callback",
			})
			if err != nil {
				return fmt.Errorf("build attributes: %w", err)
			}
			req := &genv1.ExecuteRequest{
				NodeId:      "park-reason-emission",
				InstanceId:  "park-reason-emission",
				NodeType:    "conformance",
				Attributes:  attrs,
				CallbackUrl: env.Callbacks.URL(),
			}
			outcome, err := env.Client.Execute(ctx, req)
			if err != nil {
				return fmt.Errorf("Execute: %w", err)
			}
			settled, err := conformance.AwaitTerminal(ctx, outcome, env)
			if err != nil {
				return fmt.Errorf("AwaitTerminal: %w", err)
			}
			park, ok := settled.GetOutcome().(*genv1.Outcome_Park)
			if !ok {
				return fmt.Errorf("expected Outcome_Park, got %T", settled.GetOutcome())
			}
			reason := park.Park.GetReason()
			switch reason {
			case genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK, genv1.ParkReason_PARK_REASON_SNOOZE:
			default:
				return fmt.Errorf("Park.reason outside the closed two-value set: %v", reason)
			}
			return nil
		},
	})
}
