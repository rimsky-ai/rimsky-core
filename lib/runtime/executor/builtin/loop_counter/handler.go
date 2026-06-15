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
// by schema), increments, emits named event `loop` while
// new_count < max else `done`, then closes the stream with a Success
// outcome carrying attributes_delta { count: new_count } so the
// supervisor persists the new count for next-dispatch carry-forward.
//
// @concept: executor
type Handler struct{}

func New() *Handler { return &Handler{} }

// Execute drives one dispatch. The returned error surfaces through the
// InProcessClient as a transport-level Execute failure; user-visible
// schema failures (missing/invalid `max`) are reported through the
// stream as an Error terminal so the supervisor's terminal-handling
// path (concept:error-policy) routes them like any other executor
// error.
func (h *Handler) Execute(ctx context.Context, req *genv1.ExecuteRequest, sink executor.EventSink, _ executor.HandlerContext) error {
	attrs := map[string]any{}
	if req.Attributes != nil {
		attrs = req.Attributes.AsMap()
	}
	maxRaw, ok := attrs["max"]
	if !ok {
		return h.errorTerminal(sink, "attributes_schema_invalid", "max is required")
	}
	maxN, err := asInt(maxRaw)
	if err != nil {
		return h.errorTerminal(sink, "attributes_schema_invalid", fmt.Sprintf("max: %v", err))
	}
	if maxN < 1 {
		return h.errorTerminal(sink, "attributes_schema_invalid", "max must be >= 1")
	}
	var count int
	if v, ok := attrs["count"]; ok {
		n, err := asInt(v)
		if err != nil {
			// @constraint: a non-numeric incoming `count` violates the
			// executor's declared schema (count: { type: integer }).
			// Silently defaulting to 0 would erase the loop's
			// accumulated state — a stale string carried in via a
			// schema-mismatch or a hand-crafted attribute writeback
			// would otherwise restart the counter at 1 with no
			// diagnostic. Surface the violation through the same
			// error-class the missing-max branch uses so operators
			// see the bad value.
			return h.errorTerminal(sink, "attributes_schema_invalid", fmt.Sprintf("count: %v", err))
		}
		count = n
	}
	newCount := count + 1

	var eventName string
	if newCount < maxN {
		eventName = "loop"
	} else {
		eventName = "done"
	}
	if err := sink.Send(&genv1.ExecuteEvent{
		Event: &genv1.ExecuteEvent_NamedEvent{
			NamedEvent: &genv1.NamedEvent{Name: eventName},
		},
	}); err != nil {
		return err
	}

	deltaStruct, err := structpb.NewStruct(map[string]any{"count": float64(newCount)})
	if err != nil {
		return err
	}
	return sink.Send(&genv1.ExecuteEvent{
		Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{
				Outcome: &genv1.StreamClose_Success{
					Success: &genv1.Success{
						Changed:         true,
						ChangeSummary:   fmt.Sprintf("count=%d/%d", newCount, maxN),
						AttributesDelta: deltaStruct,
					},
				},
			},
		},
	})
}

func (h *Handler) errorTerminal(sink executor.EventSink, errClass, msg string) error {
	payload, _ := structpb.NewStruct(map[string]any{"message": msg})
	return sink.Send(&genv1.ExecuteEvent{
		Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{
				Outcome: &genv1.StreamClose_Error{
					Error: &genv1.Error{ErrorClass: errClass, Payload: payload},
				},
			},
		},
	})
}

// asInt coerces the JSON-decoded `max` / `count` value (which arrives as
// float64 through structpb.AsMap) back to int. The schema constrains
// these to `type: integer`, so a non-numeric type is itself a
// schema-validation failure — surfaced as an Error terminal by the
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
