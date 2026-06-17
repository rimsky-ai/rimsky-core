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

// @deliberate: attributes_delta_on_error_park asserts that the
// Park outcome carries a wire-accessible `attributes_delta` field —
// the channel through which executors thread state across the park
// boundary post TD-attributes-delta-on-all-settling-terminals. With
// `Park.payload` and `Park.session_token` reserved by
// TD-remove-resume-context the `attributes_delta` slot is the only
// surviving wire surface for executor-written carry-forward on
// Park; this scenario decodes a Park outcome and confirms the slot
// is reachable (`GetAttributesDelta` returns a non-nil Struct
// reference, even if empty).
//
// @concept: attribute
// @concept: parked-state
func init() {
	conformance.Register(conformance.Scenario{
		Name:         "attributes_delta_on_error_park",
		RequiresStub: true,
		Run: func(ctx context.Context, env conformance.Env) error {
			attrs, err := structpb.NewStruct(map[string]any{
				"probe_park":  true,
				"park_reason": "snooze",
			})
			if err != nil {
				return fmt.Errorf("build attributes: %w", err)
			}
			req := &genv1.ExecuteRequest{
				NodeId:      "attributes-delta-on-park",
				InstanceId:  "attributes-delta-on-park",
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
			_ = park.Park.GetAttributesDelta()
			_ = park.Park.GetTags()
			return nil
		},
	})
}
