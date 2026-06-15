// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package loop_counter

import (
	"context"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/runtime/executor"
)

// captureSink is a test-only EventSink that records every Send so a
// test can replay the named-event + StreamClose sequence emitted by
// the handler without spinning up a goroutine + channel pair.
type captureSink struct {
	events []*genv1.ExecuteEvent
}

func (s *captureSink) Send(ev *genv1.ExecuteEvent) error {
	s.events = append(s.events, ev)
	return nil
}

func newReq(t *testing.T, attrs map[string]any) *genv1.ExecuteRequest {
	t.Helper()
	st, err := structpb.NewStruct(attrs)
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}
	return &genv1.ExecuteRequest{Attributes: st}
}

// dispatchAndCapture runs Handler.Execute and returns the captured
// events. The handler MUST emit exactly one NamedEvent followed by one
// StreamClose for every successful dispatch.
func dispatchAndCapture(t *testing.T, req *genv1.ExecuteRequest) []*genv1.ExecuteEvent {
	t.Helper()
	h := New()
	sink := &captureSink{}
	if err := h.Execute(context.Background(), req, sink, executor.HandlerContext{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return sink.events
}

func mustSuccessDelta(t *testing.T, ev *genv1.ExecuteEvent) map[string]any {
	t.Helper()
	sc := ev.GetStreamClose()
	if sc == nil {
		t.Fatalf("expected StreamClose, got %+v", ev)
	}
	succ := sc.GetSuccess()
	if succ == nil {
		t.Fatalf("expected Success outcome, got %+v", sc)
	}
	if succ.AttributesDelta == nil {
		t.Fatalf("expected non-nil AttributesDelta")
	}
	return succ.AttributesDelta.AsMap()
}

func TestHandler_FirstDispatch_EmitsLoopAndCount1(t *testing.T) {
	events := dispatchAndCapture(t, newReq(t, map[string]any{"max": 3}))
	if len(events) != 2 {
		t.Fatalf("expected 2 events (NamedEvent + StreamClose), got %d", len(events))
	}
	if got := events[0].GetNamedEvent().GetName(); got != "loop" {
		t.Fatalf("expected NamedEvent name=loop, got %q", got)
	}
	delta := mustSuccessDelta(t, events[1])
	if v, ok := delta["count"].(float64); !ok || int(v) != 1 {
		t.Fatalf("expected delta {count: 1}, got %+v", delta)
	}
}

func TestHandler_MidRun_EmitsLoopAndIncrementedCount(t *testing.T) {
	events := dispatchAndCapture(t, newReq(t, map[string]any{"max": 3, "count": 1}))
	if got := events[0].GetNamedEvent().GetName(); got != "loop" {
		t.Fatalf("expected NamedEvent name=loop, got %q", got)
	}
	delta := mustSuccessDelta(t, events[1])
	if v, ok := delta["count"].(float64); !ok || int(v) != 2 {
		t.Fatalf("expected delta {count: 2}, got %+v", delta)
	}
}

func TestHandler_TerminalIteration_EmitsDoneAtMax(t *testing.T) {
	events := dispatchAndCapture(t, newReq(t, map[string]any{"max": 3, "count": 2}))
	if got := events[0].GetNamedEvent().GetName(); got != "done" {
		t.Fatalf("expected NamedEvent name=done, got %q", got)
	}
	delta := mustSuccessDelta(t, events[1])
	if v, ok := delta["count"].(float64); !ok || int(v) != 3 {
		t.Fatalf("expected delta {count: 3}, got %+v", delta)
	}
}

func TestHandler_BeyondMax_EmitsDone(t *testing.T) {
	// @constraint: even when count already equals or exceeds max, the
	// handler emits `done` rather than looping forever. The cascade
	// stops firing once `done` lights up.
	events := dispatchAndCapture(t, newReq(t, map[string]any{"max": 3, "count": 5}))
	if got := events[0].GetNamedEvent().GetName(); got != "done" {
		t.Fatalf("expected NamedEvent name=done, got %q", got)
	}
}

func TestHandler_MissingMax_EmitsErrorTerminal(t *testing.T) {
	h := New()
	sink := &captureSink{}
	if err := h.Execute(context.Background(), newReq(t, map[string]any{}), sink, executor.HandlerContext{}); err != nil {
		t.Fatalf("Execute returned transport error %v; expected error terminal via stream", err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("expected 1 event (error StreamClose), got %d", len(sink.events))
	}
	errOut := sink.events[0].GetStreamClose().GetError()
	if errOut == nil {
		t.Fatalf("expected Error outcome, got %+v", sink.events[0])
	}
	if errOut.ErrorClass != "attributes_schema_invalid" {
		t.Fatalf("expected error_class attributes_schema_invalid, got %q", errOut.ErrorClass)
	}
}

func TestHandler_MaxBelowOne_EmitsErrorTerminal(t *testing.T) {
	h := New()
	sink := &captureSink{}
	if err := h.Execute(context.Background(), newReq(t, map[string]any{"max": 0}), sink, executor.HandlerContext{}); err != nil {
		t.Fatalf("Execute returned transport error %v", err)
	}
	if errOut := sink.events[0].GetStreamClose().GetError(); errOut == nil || errOut.ErrorClass != "attributes_schema_invalid" {
		t.Fatalf("expected attributes_schema_invalid terminal, got %+v", sink.events[0])
	}
}

func TestHandler_NonNumericMax_EmitsErrorTerminal(t *testing.T) {
	h := New()
	sink := &captureSink{}
	if err := h.Execute(context.Background(), newReq(t, map[string]any{"max": "three"}), sink, executor.HandlerContext{}); err != nil {
		t.Fatalf("Execute returned transport error %v", err)
	}
	if errOut := sink.events[0].GetStreamClose().GetError(); errOut == nil || errOut.ErrorClass != "attributes_schema_invalid" {
		t.Fatalf("expected attributes_schema_invalid terminal, got %+v", sink.events[0])
	}
}

func TestSchemaBytes_NonEmpty(t *testing.T) {
	if len(SchemaBytes()) == 0 {
		t.Fatalf("expected non-empty schema bytes")
	}
}

func TestDeclaredEvents_Vocabulary(t *testing.T) {
	got := DeclaredEvents()
	if len(got) != 2 {
		t.Fatalf("expected 2 declared events, got %d", len(got))
	}
	expect := map[string]bool{"loop": true, "done": true}
	for _, e := range got {
		if !expect[e] {
			t.Fatalf("unexpected event %q", e)
		}
	}
}
