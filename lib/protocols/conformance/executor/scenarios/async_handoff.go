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

// @deliberate: async_handoff exercises the AwaitAsyncCallback path:
// the unary Execute returns AwaitAsyncCallback immediately with a
// non-empty async_ack_id, the conformance receiver registers the id,
// the executor POSTs the settling outcome to
// `${callback_url}/v1/callback/{ackId}`, and the receiver delivers
// the outcome to the awaiter. Per
// TD-persist-async-callback-registry the supervisor-side registry is
// persistent; this conformance scenario asserts the protocol round
// trip without exercising the persistence guarantee (the
// async_callback_survives_restart scenario asserts that).
//
// @concept: async-callback-persistence
// @concept: executor
func init() {
	conformance.Register(conformance.Scenario{
		Name:          "async_handoff",
		RequiresStub:  true,
		RequiresAsync: true,
		Run: func(ctx context.Context, env conformance.Env) error {
			attrs, err := structpb.NewStruct(map[string]any{"probe_async": true})
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
