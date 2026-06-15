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

// retentionSeconds is the default trace retention window after a
// dispatch terminals. Exposed in capabilities; the eviction sweep
// deletes traces older than this.
const retentionSeconds uint64 = 3600

// defaultStreamIdleTimeout is the spec §2.5 close-idle default for
// StreamTrace listeners with no recent event traffic.
const defaultStreamIdleTimeout = 5 * time.Minute

// traceRecord holds the events for one dispatch plus terminal state.
//
// @concept: inertness — the structural-inertness discipline scopes
// Rimsky core's behavior toward attribute values on the wire.
// Executor-supplied trace `attributes` are produced by the executor
// itself and are fully introspectable by the executor — the
// inertness discipline does NOT restrict an executor's freedom to
// inspect, structure, or expose its own trace data.
type traceRecord struct {
	events     []*genv1.TraceEvent
	terminal   bool
	terminalAt time.Time
	// @constraint: true only when RegisterDispatch has claimed this id; AppendEvent/MarkTerminal silently no-op when false so forged dispatch ids cannot fill the ledger.
	registered bool
}

// subscriber represents one live StreamTrace listener for a dispatch.
// Per spec §2.6 events are never dropped: subscribers read events out
// of the per-dispatch slice (rec.events) at their own cursor (cursor),
// woken up by a buffered wakeup channel each time AppendEvent appends
// or MarkTerminal closes the subscription. AppendEvent never blocks
// on a slow consumer because the wakeup channel is non-blocking
// (capacity-1, coalescing pending wakeups), and there's no per-event
// channel send.
type subscriber struct {
	// @constraint: capacity 1 so non-blocking send in AppendEvent coalesces multiple pending wakes — required for the never-drop event guarantee under contention.
	wake chan struct{}
	// @constraint: closed (not sent on) by MarkTerminal so any number of waiting subscribers unblock without coordination.
	done chan struct{}
}

// ObservabilityServer is the http-node observability impl. Traces are
// kept in a per-dispatch in-memory ring with a simple TTL sweep. Live
// streaming is implemented via per-dispatch wakeup-pumped subscribers
// reading directly out of the trace slice — no per-event channel send,
// so AppendEvent never drops events under contention.
type ObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer

	mu          sync.RWMutex
	traces      map[string]*traceRecord
	subs        map[string]map[*subscriber]struct{}
	idleTimeout time.Duration
	// @constraint: set-once-at-startup before the gRPC server accepts traffic; sync.Once enforces that contract so concurrent SetHTTPBridgeURL races collapse to a single visible value.
	httpBridgeURLOnce sync.Once
	httpBridgeURL     string
}

// NewObservabilityServer constructs an empty in-memory trace store
// guarded by sync.RWMutex.
func NewObservabilityServer() *ObservabilityServer {
	return &ObservabilityServer{
		traces:      map[string]*traceRecord{},
		subs:        map[string]map[*subscriber]struct{}{},
		idleTimeout: defaultStreamIdleTimeout,
	}
}

// SetHTTPBridgeURL records the URL the executor advertises in
// ObservabilityCapabilities.http_bridge_url. Set-once at startup;
// subsequent calls are ignored. Empty value disables the hint.
func (s *ObservabilityServer) SetHTTPBridgeURL(u string) {
	s.httpBridgeURLOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.httpBridgeURL = u
	})
}

// SetIdleTimeout overrides the default StreamTrace idle close timeout.
// Pass zero to disable the timeout. Must be set before any stream
// subscribes (set-once-at-startup).
func (s *ObservabilityServer) SetIdleTimeout(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idleTimeout = d
}

// RegisterDispatch is the explicit dispatch-hook entry point. Creates
// the per-dispatch trace record so subsequent AppendEvent / MarkTerminal
// calls succeed without auto-creating records for forged ids.
//
// Per issue 13: AppendEvent and MarkTerminal now require the dispatch
// to be registered, so a fabricated dispatch_id can no longer fill the
// in-memory ledger.
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

// Capabilities reports the http-node observability surface:
// supports both GetTrace and StreamTrace, retention 1 hour, no custom
// UI.
//
// The http-node executor reads a fixed set of transport-config keys from
// the dispatched attribute bag (`url`, `method`, `headers`, `body`,
// `expect_status`, `stub_probe`, `stub_response`) and serializes the
// remaining keys as the implicit request body. To keep the
// dispatch-time `executor_schema_unavailable` gate honest about that
// "accepts any other keys" behavior we advertise a permissive open
// schema (`{"type":"object"}` with no `properties` block) —
// `graph/node.IsPermissiveExecutorSchema` recognises this as "open
// shape" and skips the readOnly-fallback leg of the unified-attribute-
// surface check.
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

// GetTrace returns the snapshot for dispatch_id. When the trace is
// past retention or evicted, returns an empty Trace with evicted=true.
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

// StreamTrace replays the snapshot then streams new events; closes
// with a synthetic trace_complete event when the dispatch terminals.
// Unknown / evicted dispatches are reported via the same evicted-shape
// trace_complete marker GetTrace returns (spec §2.6).
//
// Implementation: the subscriber is registered atomically with the
// initial cursor (0), so AppendEvent's wakeup signal always arrives
// after the cursor has been recorded. The pump loop reads events out
// of rec.events at its cursor under a short read-lock, so no events
// can fall in a gap window between snapshot and live attach.
func (s *ObservabilityServer) StreamTrace(req *genv1.StreamTraceRequest, stream genv1.ExecutorObservability_StreamTraceServer) error {
	if req.GetDispatchId() == "" {
		return status.Error(codes.InvalidArgument, "dispatch_id required")
	}
	dispatchID := req.GetDispatchId()
	sub, exists := s.subscribe(dispatchID)
	if !exists {
		// @constraint: snapshot and stream surfaces must agree on missing-dispatch behavior per executor-observability spec §2.6 — mirror GetTrace's evicted shape with a single trace_complete marker.
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
			// @constraint: MarkTerminal may append tail events under the same lock before closing sub.done — drain them before trace_complete so the never-drop guarantee holds across the terminal edge.
			tail, _ := s.drainFrom(dispatchID, cursor)
			for _, ev := range tail {
				if err := stream.Send(ev); err != nil {
					return err
				}
			}
			return stream.Send(traceCompleteEvent())
		case <-idleC:
			// @constraint: executor-observability spec §2.5 requires idle streams close cleanly with a trace_complete keepalive, not a gRPC error — operators must distinguish quiescence from failure.
			return stream.Send(traceCompleteEvent())
		case <-sub.wake:
		}
	}
}

// subscribe registers a new subscriber for the dispatch's live event
// stream. Returns (sub, false) when the dispatch is unknown to the
// ledger — callers handle that case by emitting an evicted-shape
// terminal marker. The cursor starts at 0; the StreamTrace loop reads
// events out of the trace's own slice, so atomicity comes from the
// fact that AppendEvent's wakeup signal is sent under the same lock
// that appended the event — no gap window.
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

// unsubscribe drops sub from the dispatch's subscriber set. Idempotent.
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

// drainFrom returns a copy of the dispatch's events from cursor onward
// plus the terminal flag. Caller iterates the snapshot lock-free.
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

// AppendEvent adds an event to dispatchID's trace and wakes any live
// StreamTrace subscribers. Called by executor dispatch-flow hooks.
//
// Per spec §2.6 events are never dropped: the subscriber pump reads
// directly out of the per-dispatch event slice at its own cursor, so
// the only thing the broadcast needs to do is signal that new events
// are available. The wake channel is capacity-1, so a non-blocking
// send is correct — multiple pending wakes coalesce into one drain
// pass on the receiver side.
//
// Returns silently when the dispatch has not been formally registered
// via RegisterDispatch: forged or unknown dispatch IDs cannot fill the
// in-memory ledger via the executor's bridge surface.
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

// MarkTerminal stamps a dispatch as terminal and signals every live
// subscriber. Subscribers will drain the final tail of events out of
// rec.events and then emit trace_complete. Returns silently for
// unregistered dispatch IDs (no auto-creation; see issue 13).
//
// terminalAt is captured exactly once on the first MarkTerminal call;
// subsequent calls are no-ops, so trace_complete timestamps stay
// stable across follow-on GetTrace requests.
func (s *ObservabilityServer) MarkTerminal(dispatchID string) {
	s.mu.Lock()
	rec, ok := s.traces[dispatchID]
	if !ok || !rec.registered {
		s.mu.Unlock()
		return
	}
	if rec.terminal {
		// @constraint: terminalAt is captured exactly once so trace_complete timestamps stay stable across follow-on GetTrace requests; second MarkTerminal must not overwrite.
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

// SweepEvicted removes traces whose terminal timestamp exceeds the
// retention window. Called periodically by the dispatch loop or a
// sweeper goroutine.
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

// MakeEvent is a small constructor used by the dispatch hooks to emit
// a trace event. attrs may be nil.
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

// RegisterObservability registers the http-node observability server on
// srv. Returns the server so callers can wire it into dispatch hooks.
func RegisterObservability(srv *grpc.Server) *ObservabilityServer {
	o := NewObservabilityServer()
	genv1.RegisterExecutorObservabilityServer(srv, o)
	return o
}
