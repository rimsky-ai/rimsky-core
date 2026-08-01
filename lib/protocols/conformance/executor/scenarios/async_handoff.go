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

// @decision: async-callback-persistent-registry
// @concept: executor
func init() {
	conformance.Register(conformance.Scenario{
		Name:          "async_handoff",
		RequiresStub:  true,
		RequiresAsync: true,
		Run: func(ctx context.Context, env conformance.Env) error {
			attrs, err := structpb.NewStruct(map[string]any{stubmode.AsyncProbeAttribute: true})
			if err != nil {
				return fmt.Errorf("build attributes: %w", err)
			}
			req := &genv1.ExecuteRequest{
				NodeId:      "async-handoff",
				InstanceId:  "async-handoff",
				NodeType:    "conformance",
				Attributes:  attrs,
				CallbackUrl: env.Callbacks.URL(),
			}
			outcome, err := env.Client.Execute(ctx, req)
			if err != nil {
				return fmt.Errorf("Execute: %w", err)
			}
			await, ok := outcome.GetOutcome().(*genv1.Outcome_AwaitAsync)
			if !ok {
				return fmt.Errorf("expected Outcome_AwaitAsync from unary Execute, got %T", outcome.GetOutcome())
			}
			if await.AwaitAsync.GetAsyncAckId() == "" {
				return fmt.Errorf("AwaitAsyncCallback carried empty async_ack_id")
			}
			settled, err := conformance.AwaitTerminal(ctx, outcome, env)
			if err != nil {
				return fmt.Errorf("AwaitTerminal: %w", err)
			}
			if _, isAsync := settled.GetOutcome().(*genv1.Outcome_AwaitAsync); isAsync {
				return fmt.Errorf("AwaitTerminal returned AwaitAsyncCallback; expected a settling terminal after callback")
			}
			return nil
		},
	})
}
