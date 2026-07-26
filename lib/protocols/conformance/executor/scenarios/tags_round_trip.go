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

// @concept: terminal-tag
func init() {
	conformance.Register(conformance.Scenario{
		Name:         "tags_round_trip",
		RequiresStub: true,
		Run: func(ctx context.Context, env conformance.Env) error {
			declared, err := declaredTagsFor(ctx, env.Client)
			if err != nil {
				return fmt.Errorf("DeclaredTags: %w", err)
			}
			if len(declared) == 0 {
				return nil
			}
			tag := declared[0]

			attrs, err := structpb.NewStruct(map[string]any{
				stubmode.ProbeAttribute: true,
				stubmode.TagsAttribute:  []any{tag},
			})
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
			got := success.Success.GetTags()
			if !containsTag(got, tag) {
				return fmt.Errorf("requested declared tag %q via stub_tags but the settling Outcome.Success.tags=%v did not carry it; "+
					"tags declared via Capabilities must round-trip on the settling Outcome (concept:terminal-tag)", tag, got)
			}
			return nil
		},
	})
}

func declaredTagsFor(ctx context.Context, c conformance.Client) ([]string, error) {
	dt, ok := c.(conformance.DeclaredTagsClient)
	if !ok {
		return nil, nil
	}
	return dt.DeclaredTags(ctx)
}

func containsTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}
