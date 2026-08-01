// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	conformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/stubmode"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// @concept: parked-state
func init() {
	conformance.Register(conformance.Scenario{
		Name:         "park_emission",
		RequiresStub: true,
		Run: func(ctx context.Context, env conformance.Env) error {
			resumeAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
			attrs, err := structpb.NewStruct(map[string]any{
				stubmode.ParkProbeAttribute:    true,
				stubmode.ParkResumeAtAttribute: resumeAt.Format(time.RFC3339Nano),
			})
			if err != nil {
				return fmt.Errorf("build attributes: %w", err)
			}
			req := &genv1.ExecuteRequest{
				NodeId:      "park-emission",
				InstanceId:  "park-emission",
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
			park, ok := settled.GetOutcome().(*genv1.Outcome_Park)
			if !ok {
				return fmt.Errorf("expected Outcome_Park, got %T", settled.GetOutcome())
			}
			got := park.Park.GetResumeAt()
			if got == nil {
				return fmt.Errorf("Park.resume_at not set, want %v (resume_at is required on Park)", resumeAt)
			}
			if !got.AsTime().Equal(resumeAt) {
				return fmt.Errorf("Park.resume_at=%v, want %v", got.AsTime(), resumeAt)
			}
			return nil
		},
	})
}
