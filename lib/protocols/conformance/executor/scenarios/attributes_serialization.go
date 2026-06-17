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

// @deliberate: attributes_serialization asserts the outgoing
// `attributes` Struct (set by rimsky at dispatch) deserializes
// correctly on the executor side and the Success outcome's
// `attributes_delta` is reachable through the wire decoder. Per
// TD-attributes-delta-on-all-settling-terminals the writeback channel
// on every settling terminal is `attributes_delta`; this scenario
// pins the Success-side round trip via the stub probe. The
// Error/Park `attributes_delta` shape is checked separately by
// `attributes_delta_on_error_park`.
//
// @concept: attribute
func init() {
	conformance.Register(conformance.Scenario{
		Name:         "attributes_serialization",
		RequiresStub: true,
		Run: func(ctx context.Context, env conformance.Env) error {
			attrs, err := structpb.NewStruct(map[string]any{
				"stub_probe": true,
				"echo":       "ping",
			})
			if err != nil {
				return fmt.Errorf("build attributes: %w", err)
			}
			req := &genv1.ExecuteRequest{
				NodeId:      "attributes-serialization",
				InstanceId:  "attributes-serialization",
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
			success, ok := settled.GetOutcome().(*genv1.Outcome_Success)
			if !ok {
				return fmt.Errorf("expected Outcome_Success, got %T", settled.GetOutcome())
			}
			delta := success.Success.GetAttributesDelta().AsMap()
			if _, ok := delta["stub"]; !ok {
				return fmt.Errorf("attributes_delta missing the stub marker: %#v", delta)
			}
			return nil
		},
	})
}
