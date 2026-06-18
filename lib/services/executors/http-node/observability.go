// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/executors/http-node/errorclasses"
)

const retentionSeconds uint64 = 3600

const defaultStreamIdleTimeout = 5 * time.Minute

// @concept: inertness — the structural-inertness discipline scopes
type traceRecord struct {
	events     []*genv1.TraceEvent
	terminal   bool
	terminalAt time.Time
	registered bool
}

type subscriber struct {
	wake chan struct{}
	done chan struct{}
}

type ObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer

	mu                sync.RWMutex
	traces            map[string]*traceRecord
	subs              map[string]map[*subscriber]struct{}
	idleTimeout       time.Duration
	httpBridgeURLOnce sync.Once
	httpBridgeURL     string
}

func NewObservabilityServer() *ObservabilityServer {
	return &ObservabilityServer{
		traces:      map[string]*traceRecord{},
		subs:        map[string]map[*subscriber]struct{}{},
		idleTimeout: defaultStreamIdleTimeout,
	}
}

func (s *ObservabilityServer) SetHTTPBridgeURL(u string) {
	s.httpBridgeURLOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.httpBridgeURL = u
	})
}

func (s *ObservabilityServer) SetIdleTimeout(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idleTimeout = d
}

func (s *ObservabilityServer) RegisterDispatch(dispatchID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.traces[dispatchID]
	if !ok {
		rec = &traceRecord{}
		s.traces[dispatchID] = rec
	}
	rec.registered = true
}

func (s *ObservabilityServer) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	s.mu.RLock()
	url := s.httpBridgeURL
	s.mu.RUnlock()
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              true,
		SupportsTraceStream:           true,
		RetentionAfterTerminalSeconds: retentionSeconds,
		HttpBridgeUrl:                 url,
		ExpectedAttributesSchema:      []byte(`{"type":"object"}`),
		DeclaredErrorClasses:          errorclasses.Declared(),
	}, nil
}

func (s *ObservabilityServer) GetTrace(_ context.Context, req *genv1.GetTraceRequest) (*genv1.Trace, error) {
	if req.GetDispatchId() == "" {
		return nil, status.Error(codes.InvalidArgument, "dispatch_id required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.traces[req.GetDispatchId()]
	if !ok {
		return &genv1.Trace{DispatchId: req.GetDispatchId(), Evicted: true, Complete: true}, nil
	}
	out := make([]*genv1.TraceEvent, len(rec.events))
	copy(out, rec.events)
	return &genv1.Trace{
		DispatchId: req.GetDispatchId(),
		Complete:   rec.terminal,
		Events:     out,
	}, nil
}

func (s *ObservabilityServer) StreamTrace(req *genv1.StreamTraceRequest, stream genv1.ExecutorObservability_StreamTraceServer) error {
	if req.GetDispatchId() == "" {
		return status.Error(codes.InvalidArgument, "dispatch_id required")
	}
	dispatchID := req.GetDispatchId()
	sub, exists := s.subscribe(dispatchID)
	if !exists {
		return stream.Send(traceCompleteEvent())
	}
	defer s.unsubscribe(dispatchID, sub)
	cursor := 0
	s.mu.RLock()
	idle := s.idleTimeout
	s.mu.RUnlock()
	for {
		events, terminal := s.drainFrom(dispatchID, cursor)
		cursor += len(events)
		for _, ev := range events {
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
		if terminal {
			return stream.Send(traceCompleteEvent())
		}
		var idleC <-chan time.Time
		if idle > 0 {
			t := time.NewTimer(idle)
			idleC = t.C
			defer t.Stop()
		}
		select {
		case <-stream.Context().Done():
			return nil
		case <-sub.done:
			tail, _ := s.drainFrom(dispatchID, cursor)
			for _, ev := range tail {
				if err := stream.Send(ev); err != nil {
					return err
				}
			}
			return stream.Send(traceCompleteEvent())
		case <-idleC:
			return stream.Send(traceCompleteEvent())
		case <-sub.wake:
		}
	}
}

func (s *ObservabilityServer) subscribe(dispatchID string) (*subscriber, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.traces[dispatchID]; !ok {
		return nil, false
	}
	sub := &subscriber{
		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
	}
	if s.subs[dispatchID] == nil {
		s.subs[dispatchID] = map[*subscriber]struct{}{}
	}
	s.subs[dispatchID][sub] = struct{}{}
	return sub, true
}

func (s *ObservabilityServer) unsubscribe(dispatchID string, sub *subscriber) {
	if sub == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if subs, ok := s.subs[dispatchID]; ok {
		delete(subs, sub)
	}
}

func (s *ObservabilityServer) drainFrom(dispatchID string, cursor int) ([]*genv1.TraceEvent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.traces[dispatchID]
	if !ok {
		return nil, true
	}
	if cursor >= len(rec.events) {
		return nil, rec.terminal
	}
	out := make([]*genv1.TraceEvent, len(rec.events)-cursor)
	copy(out, rec.events[cursor:])
	return out, rec.terminal
}

func (s *ObservabilityServer) AppendEvent(dispatchID string, ev *genv1.TraceEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.traces[dispatchID]
	if !ok || !rec.registered {
		return
	}
	rec.events = append(rec.events, ev)
	for sub := range s.subs[dispatchID] {
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

func (s *ObservabilityServer) MarkTerminal(dispatchID string) {
	s.mu.Lock()
	rec, ok := s.traces[dispatchID]
	if !ok || !rec.registered {
		s.mu.Unlock()
		return
	}
	if rec.terminal {
		s.mu.Unlock()
		return
	}
	rec.terminal = true
	rec.terminalAt = time.Now()
	subs := s.subs[dispatchID]
	delete(s.subs, dispatchID)
	s.mu.Unlock()
	for sub := range subs {
		close(sub.done)
	}
}

func (s *ObservabilityServer) SweepEvicted(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, rec := range s.traces {
		if rec.terminal && now.Sub(rec.terminalAt) > time.Duration(retentionSeconds)*time.Second {
			delete(s.traces, id)
		}
	}
}

func traceCompleteEvent() *genv1.TraceEvent {
	return &genv1.TraceEvent{
		EventId:   "trace_complete",
		Timestamp: timestamppb.Now(),
		Severity:  genv1.Severity_INFO,
		Category:  "trace_complete",
	}
}

func MakeEvent(eventID, parentID, category, message string, sev genv1.Severity, attrs map[string]any) *genv1.TraceEvent {
	var pb *structpb.Struct
	if attrs != nil {
		pb, _ = structpb.NewStruct(attrs)
	}
	return &genv1.TraceEvent{
		EventId:       eventID,
		ParentEventId: parentID,
		Timestamp:     timestamppb.Now(),
		Severity:      sev,
		Category:      category,
		Message:       message,
		Attributes:    pb,
	}
}

func RegisterObservability(srv *grpc.Server) *ObservabilityServer {
	o := NewObservabilityServer()
	genv1.RegisterExecutorObservabilityServer(srv, o)
	return o
}
