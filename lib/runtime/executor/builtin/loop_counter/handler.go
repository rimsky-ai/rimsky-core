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

// Handler implements executor.InProcessHandler for the loop_counter
// utility node. Reads `count` from incoming attributes (carry-forward
// yields prior, or 0 on first dispatch in scope), reads `max` (required
// by schema), increments, and returns a Success outcome tagged
// `loop` (while new_count < max) or `done` (at the cap), carrying
// attributes_delta { count: new_count } so the supervisor persists
// the new count for next-dispatch carry-forward.
//
// Per concept:terminal-tag the tag rides on the settling Success
// outcome; there is no separate event emission.
//
// @concept: executor
// @concept: terminal-tag
type Handler struct{}

func New() *Handler { return &Handler{} }

// Execute drives one dispatch. The returned error surfaces through the
// InProcessClient as a transport-level Execute failure; user-visible
// schema failures (missing/invalid `max`) are reported as an Error
// outcome so the supervisor's terminal-handling path
// (concept:error-policy) routes them like any other executor error.
func (h *Handler) Execute(ctx context.Context, req *genv1.ExecuteRequest, _ executor.HandlerContext) (*genv1.Outcome, error) {
	attrs := map[string]any{}
	if req.Attributes != nil {
		attrs = req.Attributes.AsMap()
	}
	maxRaw, ok := attrs["max"]
	if !ok {
		return errorOutcome("attributes_schema_invalid", "max is required"), nil
	}
	maxN, err := asInt(maxRaw)
	if err != nil {
		return errorOutcome("attributes_schema_invalid", fmt.Sprintf("max: %v", err)), nil
	}
	if maxN < 1 {
		return errorOutcome("attributes_schema_invalid", "max must be >= 1"), nil
	}
	var count int
	if v, ok := attrs["count"]; ok {
		n, err := asInt(v)
		if err != nil {
			// @constraint: a non-numeric incoming `count` violates the
			// executor's declared schema (count: { type: integer }).
			// Silently defaulting to 0 would erase the loop's
			// accumulated state — surface the violation through the
			// same error-class the missing-max branch uses.
			return errorOutcome("attributes_schema_invalid", fmt.Sprintf("count: %v", err)), nil
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

// asInt coerces the JSON-decoded `max` / `count` value (which arrives as
// float64 through structpb.AsMap) back to int. The schema constrains
// these to `type: integer`, so a non-numeric type is itself a
// schema-validation failure — surfaced as an Error outcome by the
// caller.
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
