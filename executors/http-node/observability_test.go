package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"

	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

func TestObservability_Capabilities(t *testing.T) {
	s := NewObservabilityServer()
	caps, err := s.GetCapabilities(context.Background(), &genv1.GetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	if !caps.GetSupportsTraceGet() || !caps.GetSupportsTraceStream() {
		t.Fatalf("expected supports_trace_get + supports_trace_stream true; got %+v", caps)
	}
	if caps.GetRetentionAfterTerminalSeconds() != retentionSeconds {
		t.Fatalf("retention = %d, want %d", caps.GetRetentionAfterTerminalSeconds(), retentionSeconds)
	}
}

func TestObservability_GetTrace_Evicted_OnUnknown(t *testing.T) {
	s := NewObservabilityServer()
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

func TestObservability_AppendAndGetTrace(t *testing.T) {
	s := NewObservabilityServer()
	s.RegisterDispatch("d1")
	s.AppendEvent("d1", MakeEvent("e1", "", "step_started", "", genv1.Severity_INFO, map[string]any{"step_id": "s1"}))
	s.AppendEvent("d1", MakeEvent("e2", "e1", "step_completed", "", genv1.Severity_INFO, map[string]any{"step_id": "s1"}))
	s.MarkTerminal("d1")
	tr, err := s.GetTrace(context.Background(), &genv1.GetTraceRequest{DispatchId: "d1"})
	if err != nil {
		t.Fatalf("GetTrace: %v", err)
	}
	if !tr.GetComplete() {
		t.Fatalf("expected complete=true after MarkTerminal")
	}
	if len(tr.GetEvents()) != 2 {
		t.Fatalf("events len = %d, want 2", len(tr.GetEvents()))
	}
}

// fakeStreamTraceServer collects events from a server-streaming
// StreamTrace call without going through the real gRPC machinery.
// It implements only the bits the server uses (Send + the embedded
// grpc.ServerStream context surface).
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

// TestObservability_StreamTrace_NoDropUnderConcurrentAppend exercises
// the race where AppendEvent calls land between snapshot capture and
// live-subscriber registration. With the bug present, those events
// would be missed by both the replay snapshot and the live channel.
//
// The test seeds a few pre-registration events, then concurrently
// fires AppendEvent goroutines while StreamTrace is starting. Once
// all goroutines finish it MarkTerminals the dispatch and waits for
// the stream to close. The set of events the stream observed must
// equal the full set ever appended.
func TestObservability_StreamTrace_NoDropUnderConcurrentAppend(t *testing.T) {
	const dispatchID = "race-d1"
	const goroutines = 16
	const eventsPer = 25
	s := NewObservabilityServer()
	s.RegisterDispatch(dispatchID)
	// Seed some events so the snapshot is non-empty at subscription
	// time — this keeps the buggy code path exercising the gap window.
	for i := 0; i < 5; i++ {
		s.AppendEvent(dispatchID, MakeEvent(fmt.Sprintf("seed-%d", i), "", "log", "", genv1.Severity_INFO, nil))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &fakeStreamTraceServer{ctx: ctx}
	streamDone := make(chan error, 1)
	go func() {
		streamDone <- s.StreamTrace(&genv1.StreamTraceRequest{DispatchId: dispatchID}, stream)
	}()

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < eventsPer; i++ {
				s.AppendEvent(dispatchID, MakeEvent(
					fmt.Sprintf("g%d-e%d", gid, i), "",
					"step_started", "",
					genv1.Severity_INFO,
					map[string]any{"g": float64(gid), "i": float64(i)},
				))
			}
		}(g)
	}
	wg.Wait()

	// MarkTerminal closes the live channel so StreamTrace returns.
	s.MarkTerminal(dispatchID)

	select {
	case <-streamDone:
	case <-time.After(5 * time.Second):
		t.Fatal("StreamTrace did not return after MarkTerminal")
	}

	got := stream.snapshot()
	// The last event is the synthetic trace_complete.
	if len(got) == 0 || got[len(got)-1].EventId != "trace_complete" {
		t.Fatalf("expected trailing trace_complete; got %d events", len(got))
	}
	// Build the set of observed event IDs minus the trailing marker.
	observed := make(map[string]struct{}, len(got)-1)
	for _, ev := range got[:len(got)-1] {
		observed[ev.GetEventId()] = struct{}{}
	}
	// Reference set: every AppendEvent ever fired against this
	// dispatch (seed + concurrent).
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

func TestObservability_SweepEvicted(t *testing.T) {
	s := NewObservabilityServer()
	s.RegisterDispatch("d1")
	s.AppendEvent("d1", MakeEvent("e1", "", "log", "hello", genv1.Severity_INFO, nil))
	s.MarkTerminal("d1")
	// Force the terminal timestamp into the past beyond retention.
	s.mu.Lock()
	s.traces["d1"].terminalAt = time.Now().Add(-2 * time.Hour)
	s.mu.Unlock()
	s.SweepEvicted(time.Now())
	tr, _ := s.GetTrace(context.Background(), &genv1.GetTraceRequest{DispatchId: "d1"})
	if !tr.GetEvicted() {
		t.Fatalf("expected evicted=true after sweep")
	}
}
