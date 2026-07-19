// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package scenarios

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	conformance "github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/executor"
	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type parkReasonProbe struct {
	id          string
	reason      string
	reasonLabel string
	reasonNote  string
	resumeAt    time.Time
	wantReason  genv1.ParkReason
}

// @concept: parked-state
func init() {
	conformance.Register(conformance.Scenario{
		Name:         "park_reason_emission",
		RequiresStub: true,
		Run: func(ctx context.Context, env conformance.Env) error {
			return runParkReasonProbe(ctx, env, parkReasonProbe{
				id:          "park-reason-emission-await-callback",
				reason:      "await_callback",
				reasonLabel: "conformance-await-callback-label",
				reasonNote:  "conformance-await-callback-note",
				wantReason:  genv1.ParkReason_PARK_REASON_AWAIT_CALLBACK,
			})
		},
	})
	conformance.Register(conformance.Scenario{
		Name:         "park_reason_emission_snooze",
		RequiresStub: true,
		Run: func(ctx context.Context, env conformance.Env) error {
			resumeAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
			return runParkReasonProbe(ctx, env, parkReasonProbe{
				id:          "park-reason-emission-snooze",
				reason:      "snooze",
				reasonLabel: "conformance-snooze-label",
				reasonNote:  "conformance-snooze-note",
				resumeAt:    resumeAt,
				wantReason:  genv1.ParkReason_PARK_REASON_SNOOZE,
			})
		},
	})
}

func runParkReasonProbe(ctx context.Context, env conformance.Env, p parkReasonProbe) error {
	attrsMap := map[string]any{
		"probe_park":        true,
		"park_reason":       p.reason,
		"park_reason_label": p.reasonLabel,
		"park_reason_note":  p.reasonNote,
	}
	if !p.resumeAt.IsZero() {
		attrsMap["park_resume_at"] = p.resumeAt.Format(time.RFC3339Nano)
	}
	attrs, err := structpb.NewStruct(attrsMap)
	if err != nil {
		return fmt.Errorf("build attributes: %w", err)
	}
	req := &genv1.ExecuteRequest{
		NodeId:      p.id,
		InstanceId:  p.id,
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
	if got := park.Park.GetReason(); got != p.wantReason {
		return fmt.Errorf("Park.reason=%v, want %v (requested park_reason=%q)", got, p.wantReason, p.reason)
	}
	if got := park.Park.GetReasonLabel(); got != p.reasonLabel {
		return fmt.Errorf("Park.reason_label=%q, want %q", got, p.reasonLabel)
	}
	if got := park.Park.GetReasonNote(); got != p.reasonNote {
		return fmt.Errorf("Park.reason_note=%q, want %q", got, p.reasonNote)
	}
	if !p.resumeAt.IsZero() {
		got := park.Park.GetResumeAt()
		if got == nil {
			return fmt.Errorf("Park.resume_at not set, want %v", p.resumeAt)
		}
		if !got.AsTime().Equal(p.resumeAt) {
			return fmt.Errorf("Park.resume_at=%v, want %v", got.AsTime(), p.resumeAt)
		}
	}
	return nil
}
