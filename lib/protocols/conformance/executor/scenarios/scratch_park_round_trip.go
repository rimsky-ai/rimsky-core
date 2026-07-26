// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package scenarios

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	conformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/stubmode"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @concept: parked-state
func init() {
	conformance.Register(conformance.Scenario{
		Name:         "scratch_park_round_trip",
		RequiresStub: true,
		Run: func(ctx context.Context, env conformance.Env) error {
			attrs, err := structpb.NewStruct(map[string]any{stubmode.ParkProbeAttribute: true})
			if err != nil {
				return fmt.Errorf("build attributes: %w", err)
			}

			firstOutcome, err := env.Client.Execute(ctx, &genv1.ExecuteRequest{
				NodeId:      "scratch-park-round-trip",
				InstanceId:  "scratch-park-round-trip",
				NodeType:    "conformance",
				Attributes:  attrs,
				CallbackUrl: env.Callbacks.URL(),
			})
			if err != nil {
				return fmt.Errorf("Execute #1: %w", err)
			}
			firstSettled, err := conformance.AwaitTerminal(ctx, firstOutcome, env)
			if err != nil {
				return fmt.Errorf("AwaitTerminal #1: %w", err)
			}
			firstPark, ok := firstSettled.GetOutcome().(*genv1.Outcome_Park)
			if !ok {
				return fmt.Errorf("expected Outcome_Park from a probe_park dispatch, got %T", firstSettled.GetOutcome())
			}
			scratch := firstPark.Park.GetScratch()
			if len(scratch) == 0 {
				return fmt.Errorf("Park carried empty scratch; an executor using the parked-state channel " +
					"(concept:parked-state) must persist non-empty scratch across a park so a resumed dispatch " +
					"can recover continuation state — this also proves the scratch field survives the async " +
					"callback decode path, not just the in-memory proto response")
			}

			secondOutcome, err := env.Client.Execute(ctx, &genv1.ExecuteRequest{
				NodeId:      "scratch-park-round-trip",
				InstanceId:  "scratch-park-round-trip",
				NodeType:    "conformance",
				Attributes:  attrs,
				CallbackUrl: env.Callbacks.URL(),
				Scratch:     scratch,
			})
			if err != nil {
				return fmt.Errorf("Execute #2 (resume-simulated dispatch carrying prior park scratch): %w", err)
			}
			secondSettled, err := conformance.AwaitTerminal(ctx, secondOutcome, env)
			if err != nil {
				return fmt.Errorf("AwaitTerminal #2: %w", err)
			}
			secondPark, ok := secondSettled.GetOutcome().(*genv1.Outcome_Park)
			if !ok {
				return fmt.Errorf("expected Outcome_Park from a second probe_park dispatch carrying prior scratch, got %T", secondSettled.GetOutcome())
			}
			if len(secondPark.Park.GetScratch()) == 0 {
				return fmt.Errorf("second Park carried empty scratch even though the dispatch supplied a " +
					"non-empty ExecuteRequest.scratch; the parked-state channel must remain populated across " +
					"repeated parks")
			}
			return nil
		},
	})
}
