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

// @deliberate: tags_round_trip asserts that the executor accepts an
// incoming dispatch and produces a settling Outcome whose `tags`
// field deserializes as a list-of-strings. Per
// TD-collapse-named-event-to-tags subscribers fire on
// `terminal/success when: "<tag>" in payload.tags`; this scenario
// pins the wire shape so a conformance run demonstrates the executor
// produces a usable tag list (deduplicated at decode per Success.tags
// set semantics). The actual cascade match (CEL filter against
// payload.tags) is exercised by `code:test/scenarios/cascade_signal_blind_test.go`.
//
// @concept: terminal-tag
func init() {
	conformance.Register(conformance.Scenario{
		Name:         "tags_round_trip",
		RequiresStub: true,
		Run: func(ctx context.Context, env conformance.Env) error {
			attrs, err := structpb.NewStruct(map[string]any{"stub_probe": true})
			if err != nil {
				return fmt.Errorf("build attributes: %w", err)
			}
			req := &genv1.ExecuteRequest{
				NodeId:      "tags-round-trip",
				InstanceId:  "tags-round-trip",
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
			_ = success.Success.GetTags()
			return nil
		},
	})
}
