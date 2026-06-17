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

// @deliberate: malformed_attributes asserts the executor rejects a
// dispatch carrying a reserved malformed-shape marker by settling
// with Outcome{Error}; the error_class string is the executor's own
// (e.g. agent/attribute_invalid for claude-agent). Per
// TD-execute-rpc-unary the rejection rides the unary outcome — the
// stream-close discriminator is gone.
//
// @concept: executor
func init() {
	conformance.Register(conformance.Scenario{
		Name:         "malformed_attributes",
		RequiresStub: true,
		Run: func(ctx context.Context, env conformance.Env) error {
			attrs, err := structpb.NewStruct(map[string]any{"_invalid": true})
			if err != nil {
				return fmt.Errorf("build attributes: %w", err)
			}
			req := &genv1.ExecuteRequest{
				NodeId:      "malformed-attributes",
				InstanceId:  "malformed-attributes",
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
			errOut, ok := settled.GetOutcome().(*genv1.Outcome_Error)
			if !ok {
				return fmt.Errorf("expected Outcome_Error for malformed-shape marker, got %T", settled.GetOutcome())
			}
			if errOut.Error.GetErrorClass() == "" {
				return fmt.Errorf("Error.error_class is empty")
			}
			return nil
		},
	})
}
