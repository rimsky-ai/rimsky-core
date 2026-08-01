// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	conformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/stubmode"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @concept: attribute
func init() {
	conformance.Register(conformance.Scenario{
		Name:         "attributes_serialization",
		RequiresStub: true,
		Run: func(ctx context.Context, env conformance.Env) error {
			attrs, err := structpb.NewStruct(map[string]any{
				stubmode.ProbeAttribute: true,
				"nested": map[string]any{
					"list":    []any{1.0, "two", true, nil},
					"unicode": "héllo wörld 世界",
					"number":  -12.5,
				},
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
			if _, ok := delta[stubmode.ResponseAttribute]; !ok {
				return fmt.Errorf("attributes_delta missing the stub marker: %#v", delta)
			}
			return nil
		},
	})
}
