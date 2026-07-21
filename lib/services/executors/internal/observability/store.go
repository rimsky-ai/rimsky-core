// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package observability

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

const RetentionSeconds uint64 = 3600

const DefaultStreamIdleTimeout = 5 * time.Minute

// @concept: inertness
type traceRecord struct {
	events     []*genv1.TraceEvent
	terminal   bool
	terminalAt time.Time
}

type subscriber struct {
	wake chan struct{}
	done chan struct{}
}

type Store struct {
	mu          sync.RWMutex
	traces      map[string]*traceRecord
	subs        map[string]map[*subscriber]struct{}
	idleTimeout time.Duration
}

func NewStore() *Store {
	return &Store{
		traces:      map[string]*traceRecord{},
		subs:        map[string]map[*subscriber]struct{}{},
		idleTimeout: DefaultStreamIdleTimeout,
	}
}

func (s *Store) SetIdleTimeout(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idleTimeout = d
}

func (s *Store) RegisterDispatch(nodeRunID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.traces[nodeRunID]; !ok {
		s.traces[nodeRunID] = &traceRecord{}
	}
}

func (s *Store) GetTrace(_ context.Context, req *genv1.GetTraceRequest) (*genv1.Trace, error) {
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

func (s *Store) StreamTrace(req *genv1.StreamTraceRequest, stream genv1.ExecutorObservability_StreamTraceServer) error {
	if req.GetDispatchId() == "" {
		return status.Error(codes.InvalidArgument, "dispatch_id required")
	}
	nodeRunID := req.GetDispatchId()
	sub, exists := s.subscribe(nodeRunID)
	if !exists {
		return stream.Send(TraceCompleteEvent())
	}
	defer s.unsubscribe(nodeRunID, sub)
	cursor := 0
	s.mu.RLock()
	idle := s.idleTimeout
	s.mu.RUnlock()
	for {
		events, terminal := s.drainFrom(nodeRunID, cursor)
		cursor += len(events)
		for _, ev := range events {
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
		if terminal {
			return stream.Send(TraceCompleteEvent())
		}
		var idleC <-chan time.Time
		var t *time.Timer
		if idle > 0 {
			t = time.NewTimer(idle)
			idleC = t.C
		}
		select {
		case <-stream.Context().Done():
			if t != nil {
				t.Stop()
			}
			return nil
		case <-sub.done:
			if t != nil {
				t.Stop()
			}
			tail, _ := s.drainFrom(nodeRunID, cursor)
			for _, ev := range tail {
				if err := stream.Send(ev); err != nil {
					return err
				}
			}
			return stream.Send(TraceCompleteEvent())
		case <-idleC:
			return stream.Send(TraceIdleTimeoutEvent())
		case <-sub.wake:
			if t != nil {
				t.Stop()
			}
		}
	}
}

func (s *Store) subscribe(nodeRunID string) (*subscriber, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.traces[nodeRunID]; !ok {
		return nil, false
	}
	sub := &subscriber{
		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
	}
	if s.subs[nodeRunID] == nil {
		s.subs[nodeRunID] = map[*subscriber]struct{}{}
	}
	s.subs[nodeRunID][sub] = struct{}{}
	return sub, true
}

func (s *Store) unsubscribe(nodeRunID string, sub *subscriber) {
	if sub == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if subs, ok := s.subs[nodeRunID]; ok {
		delete(subs, sub)
	}
}

func (s *Store) drainFrom(nodeRunID string, cursor int) ([]*genv1.TraceEvent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.traces[nodeRunID]
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

func (s *Store) AppendEvent(nodeRunID string, ev *genv1.TraceEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.traces[nodeRunID]
	if !ok {
		return
	}
	rec.events = append(rec.events, ev)
	for sub := range s.subs[nodeRunID] {
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

func (s *Store) MarkTerminal(nodeRunID string) {
	s.mu.Lock()
	rec, ok := s.traces[nodeRunID]
	if !ok {
		s.mu.Unlock()
		return
	}
	if rec.terminal {
		s.mu.Unlock()
		return
	}
	rec.terminal = true
	rec.terminalAt = time.Now()
	subs := s.subs[nodeRunID]
	delete(s.subs, nodeRunID)
	s.mu.Unlock()
	for sub := range subs {
		close(sub.done)
	}
}

func (s *Store) SweepEvicted(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, rec := range s.traces {
		if rec.terminal && now.Sub(rec.terminalAt) > time.Duration(RetentionSeconds)*time.Second {
			delete(s.traces, id)
		}
	}
}

func (s *Store) forceTerminalAt(nodeRunID string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.traces[nodeRunID]; ok {
		rec.terminalAt = at
	}
}

func TraceCompleteEvent() *genv1.TraceEvent {
	return &genv1.TraceEvent{
		EventId:   "trace_complete",
		Timestamp: timestamppb.Now(),
		Severity:  genv1.Severity_INFO,
		Category:  "trace_complete",
	}
}

func TraceIdleTimeoutEvent() *genv1.TraceEvent {
	return &genv1.TraceEvent{
		EventId:   "trace_idle_timeout",
		Timestamp: timestamppb.Now(),
		Severity:  genv1.Severity_INFO,
		Category:  "trace_idle_timeout",
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

func MountTraceBridge(mux *http.ServeMux, store *Store) {
	mux.HandleFunc("/observability/v1/trace/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/observability/v1/trace/")
		isStream := strings.HasSuffix(path, "/stream")
		nodeRunID := strings.TrimSuffix(path, "/stream")
		if nodeRunID == "" || strings.Contains(nodeRunID, "/") {
			http.Error(w, "bad dispatch_id", http.StatusBadRequest)
			return
		}
		if isStream {
			HandleTraceStreamHTTP(w, r, store, nodeRunID)
			return
		}
		trace, err := store.GetTrace(r.Context(), &genv1.GetTraceRequest{DispatchId: nodeRunID})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		WriteProtoJSON(w, trace)
	})
}

func HandleTraceStreamHTTP(w http.ResponseWriter, r *http.Request, store *Store, nodeRunID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	errClientGone := errors.New("sse: client gone")
	send := func(ev *genv1.TraceEvent) error {
		b, _ := protojson.Marshal(ev)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return errClientGone
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}
	sub, exists := store.subscribe(nodeRunID)
	if !exists {
		_ = send(TraceCompleteEvent())
		return
	}
	defer store.unsubscribe(nodeRunID, sub)
	cursor := 0
	store.mu.RLock()
	idle := store.idleTimeout
	store.mu.RUnlock()
	for {
		events, terminal := store.drainFrom(nodeRunID, cursor)
		cursor += len(events)
		for _, ev := range events {
			if err := send(ev); err != nil {
				return
			}
		}
		if terminal {
			_ = send(TraceCompleteEvent())
			return
		}
		var idleC <-chan time.Time
		var t *time.Timer
		if idle > 0 {
			t = time.NewTimer(idle)
			idleC = t.C
		}
		select {
		case <-r.Context().Done():
			if t != nil {
				t.Stop()
			}
			return
		case <-sub.done:
			if t != nil {
				t.Stop()
			}
			tail, _ := store.drainFrom(nodeRunID, cursor)
			for _, ev := range tail {
				if err := send(ev); err != nil {
					return
				}
			}
			_ = send(TraceCompleteEvent())
			return
		case <-idleC:
			_ = send(TraceIdleTimeoutEvent())
			return
		case <-sub.wake:
			if t != nil {
				t.Stop()
			}
		}
	}
}

func WriteProtoJSON(w http.ResponseWriter, m proto.Message) {
	w.Header().Set("Content-Type", "application/json")
	b, err := protojson.Marshal(m)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(b)
}
