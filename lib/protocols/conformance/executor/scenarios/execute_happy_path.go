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

// @concept: executor
func init() {
	conformance.Register(conformance.Scenario{
		Name:         "execute_happy_path",
		RequiresStub: true,
		Run: func(ctx context.Context, env conformance.Env) error {
			attrs, err := structpb.NewStruct(map[string]any{stubmode.ProbeAttribute: true})
			if err != nil {
				return fmt.Errorf("build attributes: %w", err)
			}
			schema, err := structpb.NewStruct(map[string]any{
				"type":       "object",
				"properties": map[string]any{stubmode.ProbeAttribute: map[string]any{"type": "boolean"}},
			})
			if err != nil {
				return fmt.Errorf("build attributes_schema: %w", err)
			}
			req := &genv1.ExecuteRequest{
				NodeId:           "execute-happy-path",
				InstanceId:       "execute-happy-path",
				NodeType:         "conformance",
				Attributes:       attrs,
				AttributesSchema: schema,
				CallbackUrl:      env.Callbacks.URL(),
				CancelToken:      "conformance-execute-happy-path-cancel-token",
				DispatchId:       "conformance-execute-happy-path-dispatch",
				RunScopeId:       "conformance-execute-happy-path-run-scope",
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
			if !stubmode.ConfirmsStub(delta) {
				return fmt.Errorf("expected attributes_delta.stub=true, got %#v", delta[stubmode.ResponseAttribute])
			}
			return nil
		},
	})
}
