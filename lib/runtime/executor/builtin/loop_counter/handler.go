// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package loop_counter

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
)

// @concept: executor
// @concept: terminal-tag
type Handler struct{}

func New() *Handler { return &Handler{} }

func (h *Handler) Execute(ctx context.Context, req *genv1.ExecuteRequest, _ executor.HandlerContext) (*genv1.Outcome, error) {
	attrs := map[string]any{}
	if req.Attributes != nil {
		attrs = req.Attributes.AsMap()
	}
	maxRaw, ok := attrs["max"]
	if !ok {
		return errorOutcome(AttributesSchemaInvalidClass, "max is required"), nil
	}
	maxN, err := asInt(maxRaw)
	if err != nil {
		return errorOutcome(AttributesSchemaInvalidClass, fmt.Sprintf("max: %v", err)), nil
	}
	if maxN < 1 {
		return errorOutcome(AttributesSchemaInvalidClass, "max must be >= 1"), nil
	}
	var count int
	if v, ok := attrs["count"]; ok {
		n, err := asInt(v)
		if err != nil {
			return errorOutcome(AttributesSchemaInvalidClass, fmt.Sprintf("count: %v", err)), nil
		}
		count = n
	}
	newCount := count + 1

	tag := "loop"
	if newCount >= maxN {
		tag = "done"
	}
	deltaStruct, err := structpb.NewStruct(map[string]any{"count": float64(newCount)})
	if err != nil {
		return nil, err
	}
	return &genv1.Outcome{
		Outcome: &genv1.Outcome_Success{
			Success: &genv1.Success{
				Changed:         true,
				ChangeSummary:   fmt.Sprintf("count=%d/%d", newCount, maxN),
				AttributesDelta: deltaStruct,
				Tags:            []string{tag},
			},
		},
	}, nil
}

func errorOutcome(errClass, msg string) *genv1.Outcome {
	payload, _ := structpb.NewStruct(map[string]any{"message": msg})
	return &genv1.Outcome{
		Outcome: &genv1.Outcome_Error{
			Error: &genv1.Error{ErrorClass: errClass, Payload: payload},
		},
	}
}

func asInt(v any) (int, error) {
	switch x := v.(type) {
	case float64:
		return int(x), nil
	case int:
		return x, nil
	case int64:
		return int(x), nil
	default:
		return 0, fmt.Errorf("not a number: %T", v)
	}
}
