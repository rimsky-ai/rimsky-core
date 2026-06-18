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

// @decision: async-callback-persistent-registry
func init() {
	conformance.Register(conformance.Scenario{
		Name:          "async_callback_survives_restart",
		RequiresStub:  true,
		RequiresAsync: true,
		Run: func(ctx context.Context, env conformance.Env) error {
			attrs, err := structpb.NewStruct(map[string]any{"probe_async": true})
			if err != nil {
				return fmt.Errorf("build attributes: %w", err)
			}
			req := &genv1.ExecuteRequest{
				NodeId:      "async-callback-survives-restart",
				InstanceId:  "async-callback-survives-restart",
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
				return fmt.Errorf("expected Outcome_AwaitAsync, got %T", outcome.GetOutcome())
			}
			ackID := await.AwaitAsync.GetAsyncAckId()
			if ackID == "" {
				return fmt.Errorf("AwaitAsyncCallback async_ack_id is empty; supervisor cannot route the callback")
			}
			settled, err := conformance.AwaitTerminal(ctx, outcome, env)
			if err != nil {
				return fmt.Errorf("AwaitTerminal: %w", err)
			}
			if _, isAsync := settled.GetOutcome().(*genv1.Outcome_AwaitAsync); isAsync {
				return fmt.Errorf("expected a settling terminal after async callback, got AwaitAsyncCallback again")
			}
			return nil
		},
	})
}
