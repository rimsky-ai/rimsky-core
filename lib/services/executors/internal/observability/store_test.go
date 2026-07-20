// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package observability

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestStoreGetTraceEvictedOnUnknown(t *testing.T) {
	s := NewStore()
	tr, err := s.GetTrace(context.Background(), &genv1.GetTraceRequest{DispatchId: "ghost"})
	if err != nil {
		t.Fatalf("GetTrace: %v", err)
	}
	if !tr.GetEvicted() {
		t.Fatalf("expected evicted=true for unknown dispatch")
	}
	if !tr.GetComplete() {
		t.Fatalf("expected complete=true for evicted dispatch")
	}
}

func TestStoreAppendAndGetTrace(t *testing.T) {
	s := NewStore()
	s.RegisterDispatch("d1")
	emitted := []*genv1.TraceEvent{
		MakeEvent("e1", "", "step_started", "", genv1.Severity_INFO, map[string]any{"step_id": "s1"}),
		MakeEvent("e2", "e1", "step_completed", "", genv1.Severity_INFO, map[string]any{"step_id": "s1"}),
	}
	for _, ev := range emitted {
		s.AppendEvent("d1", ev)
	}
	s.MarkTerminal("d1")
	tr, err := s.GetTrace(context.Background(), &genv1.GetTraceRequest{DispatchId: "d1"})
	if err != nil {
		t.Fatalf("GetTrace: %v", err)
	}
	if !tr.GetComplete() {
		t.Fatalf("expected complete=true after MarkTerminal")
	}
	got := tr.GetEvents()
	if len(got) != len(emitted) {
		t.Fatalf("events len = %d, want %d", len(got), len(emitted))
	}
	for i, want := range emitted {
		if !proto.Equal(got[i], want) {
			t.Fatalf("event[%d] = %+v, want %+v (history must return exactly what was emitted, in emission order)", i, got[i], want)
		}
	}
}

type fakeStreamTraceServer struct {
	ctx    context.Context
	mu     sync.Mutex
	events []*genv1.TraceEvent
}

func (f *fakeStreamTraceServer) Send(ev *genv1.TraceEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
	return nil
}

func (f *fakeStreamTraceServer) Context() context.Context     { return f.ctx }
func (f *fakeStreamTraceServer) SetHeader(metadata.MD) error  { return nil }
func (f *fakeStreamTraceServer) SendHeader(metadata.MD) error { return nil }
func (f *fakeStreamTraceServer) SetTrailer(metadata.MD)       {}
func (f *fakeStreamTraceServer) SendMsg(_ any) error          { return nil }
func (f *fakeStreamTraceServer) RecvMsg(_ any) error          { return nil }

func (f *fakeStreamTraceServer) snapshot() []*genv1.TraceEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*genv1.TraceEvent, len(f.events))
	copy(out, f.events)
	return out
}

func TestStoreStreamTraceNoDropUnderConcurrentAppend(t *testing.T) {
	const nodeRunID = "race-d1"
	const goroutines = 16
	const eventsPer = 25
	s := NewStore()
	s.RegisterDispatch(nodeRunID)
	for i := 0; i < 5; i++ {
		s.AppendEvent(nodeRunID, MakeEvent(fmt.Sprintf("seed-%d", i), "", "log", "", genv1.Severity_INFO, nil))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &fakeStreamTraceServer{ctx: ctx}
	streamDone := make(chan error, 1)
	go func() {
		streamDone <- s.StreamTrace(&genv1.StreamTraceRequest{DispatchId: nodeRunID}, stream)
	}()

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < eventsPer; i++ {
				s.AppendEvent(nodeRunID, MakeEvent(
					fmt.Sprintf("g%d-e%d", gid, i), "",
					"step_started", "",
					genv1.Severity_INFO,
					map[string]any{"g": float64(gid), "i": float64(i)},
				))
			}
		}(g)
	}
	wg.Wait()

	s.MarkTerminal(nodeRunID)

	select {
	case <-streamDone:
	case <-time.After(5 * time.Second):
		t.Fatal("StreamTrace did not return after MarkTerminal")
	}

	got := stream.snapshot()
	if len(got) == 0 || got[len(got)-1].EventId != "trace_complete" {
		t.Fatalf("expected trailing trace_complete; got %d events", len(got))
	}
	observed := make(map[string]struct{}, len(got)-1)
	for _, ev := range got[:len(got)-1] {
		observed[ev.GetEventId()] = struct{}{}
	}
	expected := make(map[string]struct{})
	for i := 0; i < 5; i++ {
		expected[fmt.Sprintf("seed-%d", i)] = struct{}{}
	}
	for g := 0; g < goroutines; g++ {
		for i := 0; i < eventsPer; i++ {
			expected[fmt.Sprintf("g%d-e%d", g, i)] = struct{}{}
		}
	}
	for id := range expected {
		if _, ok := observed[id]; !ok {
			t.Fatalf("StreamTrace dropped event %s under concurrent append", id)
		}
	}
}

func TestStoreStreamTrace_IdleTimeoutIsDistinctFromGenuineCompletion(t *testing.T) {
	const nodeRunID = "idle-d1"
	s := NewStore()
	s.SetIdleTimeout(5 * time.Millisecond)
	s.RegisterDispatch(nodeRunID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &fakeStreamTraceServer{ctx: ctx}

	streamDone := make(chan error, 1)
	go func() {
		streamDone <- s.StreamTrace(&genv1.StreamTraceRequest{DispatchId: nodeRunID}, stream)
	}()

	select {
	case <-streamDone:
	case <-time.After(5 * time.Second):
		t.Fatal("StreamTrace did not return after the idle timeout elapsed")
	}

	got := stream.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected exactly one sentinel event, got %d: %+v", len(got), got)
	}
	if got[0].EventId == "trace_complete" || got[0].Category == "trace_complete" {
		t.Fatalf("idle timeout must not send the same trace_complete sentinel as a genuine terminal "+
			"(GetTrace still reports Complete=false); got %+v", got[0])
	}
	if got[0].EventId != "trace_idle_timeout" {
		t.Fatalf("EventId = %q, want trace_idle_timeout", got[0].EventId)
	}

	trace, err := s.GetTrace(context.Background(), &genv1.GetTraceRequest{DispatchId: nodeRunID})
	if err != nil {
		t.Fatalf("GetTrace: %v", err)
	}
	if trace.GetComplete() {
		t.Fatal("GetTrace reports Complete=true after only an idle timeout, not a genuine terminal " +
			"— this is exactly the inconsistency the distinct sentinel must prevent a consumer from missing")
	}
}

func TestStoreSweepEvicted(t *testing.T) {
	s := NewStore()
	s.RegisterDispatch("d1")
	s.AppendEvent("d1", MakeEvent("e1", "", "log", "hello", genv1.Severity_INFO, nil))
	s.MarkTerminal("d1")
	s.forceTerminalAt("d1", time.Now().Add(-2*time.Hour))
	s.SweepEvicted(time.Now())
	tr, _ := s.GetTrace(context.Background(), &genv1.GetTraceRequest{DispatchId: "d1"})
	if !tr.GetEvicted() {
		t.Fatalf("expected evicted=true after sweep")
	}
}
