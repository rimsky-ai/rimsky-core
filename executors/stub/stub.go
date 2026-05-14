// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package stub is a test-double Executor implementation in the
// Meszaros sense — scripted canned outcomes for tests, conformance,
// and no-op demos. NOT a skeleton template for writing your own
// executor; see executors/http-node and executors/claude-agent for
// reference implementations.
//
// Three primary uses:
//   - executors/stub/cmd — standalone gRPC binary used by the
//     quickstart and smoke deployments as a no-op executor.
//   - executors/stub/stubtest — wrapper for in-process scenario tests
//     in test/scenarios/. Tests script per-node-type behavior via
//     Stub.WhenType("…").Success/Error/Park/… and assert on the
//     supervisor's reaction.
//   - rimsky-executor-conformance — known-good target for protocol
//     conformance checks (when run with --require-stub-mode).
//
// EnableStubMode shortcuts scripted behavior with immediate-success
// outcomes plus StubAttributesFor(node_type)-shaped attributes_delta;
// the quickstart and conformance harness use this mode.
package stub

import (
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

type terminalKind int

const (
	termSuccess terminalKind = iota
	termError
	termAsync
	// termPark scripts a Park terminal event (parking the node until
	// resume_at). Used by L2 conformance and by E6 / H3 scenario tests.
	termPark
)

// namedEventEmit captures one NamedEvent emission scripted before a
// terminal verdict. Per plan L2/H1, the stub may emit zero or more
// NamedEvent records before the terminal so test fixtures and the
// rimsky-executor-conformance binary can exercise the streaming-events path.
type namedEventEmit struct {
	Name    string
	Payload []byte
}

type script struct {
	terminal          terminalKind
	result            any
	changed           bool
	changeSum         string
	errorClass        string
	payload           any
	asyncAckID        string
	asyncCompletionMs int64
	heartbeats        int
	delay             time.Duration
	// Park-terminal scripted fields.
	parkReason       string
	parkPayload      []byte
	parkResumeAt     time.Time // zero ⇒ indefinite park (no resume_at)
	parkSessionToken string
	// Named-event emissions scripted before the terminal. Emitted in
	// order, between heartbeats and the terminal event.
	namedEvents []namedEventEmit
}

// Stub is a scripted Executor server for tests.
type Stub struct {
	genv1.UnimplementedExecutorServer
	mu       sync.Mutex
	scripts  map[string]*script
	stubMode bool
	observed []ObservedRequest
}

// ObservedRequest captures the dispatch-time fields a test may want to assert
// against. Per spec §12.1 the executor receives `attributes` (rimsky-populated
// per-run typed attributes) and opaque `userdata`; the stub records both
// per call so tests can verify the supervisor wired them through correctly.
//
// CallbackURL and CancelToken are recorded so scenario tests exercising
// the §12.5 incremental-writeback path can POST per-field deltas back to
// the supervisor with the same auth shape a real executor would use.
type ObservedRequest struct {
	NodeID      string
	InstanceID  string
	NodeType    string
	Attributes  map[string]any
	Userdata    map[string]any
	CallbackURL string
	CancelToken string
}

// New constructs a Stub with no scripted node types registered.
func New() *Stub { return &Stub{scripts: map[string]*script{}} }

// EnableStubMode switches the Stub into immediate-success mode. In this mode
// every Execute call short-circuits scripted behavior and returns a single
// terminal StreamClose with Success outcome carrying `changed: true` and
// `attributes_delta` populated from StubAttributesFor(node_type). Used by
// conformance probes and end-to-end stack tests where the executor surface is
// exercised but not the application logic.
func (s *Stub) EnableStubMode() *Stub {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stubMode = true
	return s
}

// Observed returns a snapshot of every ExecuteRequest the stub has seen since
// construction. Safe to call concurrently with in-flight Execute calls.
func (s *Stub) Observed() []ObservedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ObservedRequest, len(s.observed))
	copy(out, s.observed)
	return out
}

// TypeBuilder registers scripted behavior for nodes of a specific type.
type TypeBuilder struct {
	s   *Stub
	typ string
}

// WhenType begins scripting behavior for the given node_type. Default
// terminal is a Success outcome with changed=true.
func (s *Stub) WhenType(t string) *TypeBuilder {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := &script{terminal: termSuccess, changed: true}
	s.scripts[t] = sc
	return &TypeBuilder{s: s, typ: t}
}

// Success configures the scripted terminal as a StreamClose with a
// Success outcome on the wire.
func (b *TypeBuilder) Success(result any, changed bool, changeSummary string) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.s.scripts[b.typ]
	sc.terminal, sc.result, sc.changed, sc.changeSum = termSuccess, result, changed, changeSummary
	return b
}

// Error configures the scripted terminal as a StreamClose with an Error
// outcome on the wire. The `class` argument becomes the wire-level
// error_class. To script the executor-blocked path, pass
// "executor_blocked" as the class.
func (b *TypeBuilder) Error(class string, payload any) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.s.scripts[b.typ]
	sc.terminal, sc.errorClass, sc.payload = termError, class, payload
	return b
}

// AwaitAsyncCallback configures the scripted terminal as an AwaitAsyncCallback event.
func (b *TypeBuilder) AwaitAsyncCallback(ackID string, completionMs int64) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.s.scripts[b.typ]
	sc.terminal, sc.asyncAckID, sc.asyncCompletionMs = termAsync, ackID, completionMs
	return b
}

// Park configures the scripted terminal as a Park event.
// resumeAt may be zero (indefinite park; resumes only via external
// invalidate). reason may be empty (permitted but logged WARN supervisor-
// side per plan E1). sessionToken is opaque to rimsky and round-tripped
// to the executor on resume via ResumeContext.session_token.
//
// Per plan A3 / E1 / L2.
func (b *TypeBuilder) Park(reason string, payload []byte, resumeAt time.Time, sessionToken string) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.s.scripts[b.typ]
	sc.terminal = termPark
	sc.parkReason = reason
	sc.parkPayload = payload
	sc.parkResumeAt = resumeAt
	sc.parkSessionToken = sessionToken
	return b
}

// EmitNamedEvent scripts a NamedEvent emission before the terminal.
// Multiple calls accumulate; events are emitted in the order recorded,
// after heartbeats and before the terminal verdict. Per plan L2/H1.
func (b *TypeBuilder) EmitNamedEvent(name string, payload []byte) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.s.scripts[b.typ]
	sc.namedEvents = append(sc.namedEvents, namedEventEmit{Name: name, Payload: payload})
	return b
}

// Heartbeats adds N extra heartbeat events before the terminal event.
func (b *TypeBuilder) Heartbeats(n int) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	b.s.scripts[b.typ].heartbeats = n
	return b
}

// Delay sleeps for d before emitting each event. Useful for silence-detection
// scenarios and context-cancellation tests.
func (b *TypeBuilder) Delay(d time.Duration) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	b.s.scripts[b.typ].delay = d
	return b
}

// Execute implements genv1.ExecutorServer by streaming scripted events.
// Records the incoming request (id/type/attributes/userdata) for test
// inspection. If stub mode is enabled, short-circuits to an immediate
// StreamClose with Success outcome keyed by node_type via StubAttributesFor.
func (s *Stub) Execute(req *genv1.ExecuteRequest, stream genv1.Executor_ExecuteServer) error {
	s.mu.Lock()
	s.observed = append(s.observed, ObservedRequest{
		NodeID:      req.GetNodeId(),
		InstanceID:  req.GetInstanceId(),
		NodeType:    req.GetNodeType(),
		Attributes:  req.GetAttributes().AsMap(),
		Userdata:    req.GetUserdata().AsMap(),
		CallbackURL: req.GetCallbackUrl(),
		CancelToken: req.GetCancelToken(),
	})
	stubMode := s.stubMode
	sc, ok := s.scripts[req.NodeType]
	s.mu.Unlock()

	if stubMode {
		delta, err := structpb.NewStruct(StubAttributesFor(req.GetNodeType()))
		if err != nil {
			return err
		}
		return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
				AttributesDelta: delta,
				Changed:         true,
				ChangeSummary:   "stub",
			}}},
		}})
	}

	if !ok {
		return fmt.Errorf("stub: no script for node_type %q", req.NodeType)
	}

	// heartbeats (at least one before terminal)
	for i := 0; i < sc.heartbeats+1; i++ {
		if sc.delay > 0 {
			select {
			case <-time.After(sc.delay):
			case <-stream.Context().Done():
				return stream.Context().Err()
			}
		}
		if err := stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Heartbeat{Heartbeat: &genv1.Heartbeat{
			TimestampMs: time.Now().UnixMilli(),
			Note:        fmt.Sprintf("stub heartbeat %d", i+1),
		}}}); err != nil {
			return err
		}
	}

	// Scripted NamedEvent emissions before the terminal (plan L2 / H1).
	for _, ne := range sc.namedEvents {
		if err := stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_NamedEvent{NamedEvent: &genv1.NamedEvent{
			Name:    ne.Name,
			Payload: ne.Payload,
		}}}); err != nil {
			return err
		}
	}

	// terminal
	switch sc.terminal {
	case termSuccess:
		// Success carries `attributes_delta` (a Struct). The stub's
		// `result` API is preserved for test convenience and mapped
		// to an AttributesDelta map when the value is a map[string]any
		// (the only realistic shape — the supervisor merges it into
		// the resolved attribute object). Non-map / nil values are
		// dropped; the executor side has no other field to emit them on.
		delta, err := toStruct(sc.result)
		if err != nil {
			return err
		}
		return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
				AttributesDelta: delta, Changed: sc.changed, ChangeSummary: sc.changeSum,
			}}},
		}})
	case termError:
		v, err := toStruct(sc.payload)
		if err != nil {
			return err
		}
		return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Error{Error: &genv1.Error{
				ErrorClass: sc.errorClass, Payload: v,
			}}},
		}})
	case termAsync:
		return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_AwaitAsync{AwaitAsync: &genv1.AwaitAsyncCallback{
				AsyncAckId: sc.asyncAckID, ExpectedCompletionMs: sc.asyncCompletionMs,
			}}},
		}})
	case termPark:
		park := &genv1.Park{
			Reason:       sc.parkReason,
			Payload:      sc.parkPayload,
			SessionToken: sc.parkSessionToken,
		}
		if !sc.parkResumeAt.IsZero() {
			park.ResumeAt = timestamppb.New(sc.parkResumeAt)
		}
		return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Park{Park: park}},
		}})
	}
	return nil
}

// stubFixtures maps node_type to a default attributes_delta the stub
// returns when stub mode is enabled. Kept small and illustrative on
// purpose — real consumers register their own fixtures (or use the empty
// default) rather than relying on this map. Any node_type absent from the
// map gets `{}` from StubAttributesFor.
var stubFixtures = map[string]map[string]any{
	"items.fetch":    {"items": []any{}, "fetched_at": "1970-01-01T00:00:00Z"},
	"items.classify": {"category": "unclassified"},
}

// StubAttributesFor returns the default attributes_delta for a node_type
// when the stub is running in stub mode. Returns an empty (non-nil) map
// for unknown node_types so the resulting structpb.Struct has zero fields
// (the supervisor treats an empty Struct as a no-op writeback).
//
// Returns a fresh map on every call; callers may mutate the result without
// affecting subsequent dispatches.
func StubAttributesFor(nodeType string) map[string]any {
	src, ok := stubFixtures[nodeType]
	if !ok {
		return map[string]any{}
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// toStruct converts an arbitrary input into a structpb.Struct for use as
// Success.AttributesDelta or Error.Payload on the StreamClose outcome.
// Nil inputs return nil — the supervisor treats nil delta as "no writeback".
// map[string]any inputs are converted directly via structpb.NewStruct.
// Other non-nil inputs are wrapped as `{value: <fmt.Sprint(v)>}` so the
// test fixture can still observe scalar values without adding fields to
// the proto.
func toStruct(v any) (*structpb.Struct, error) {
	if v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		// Wrap non-map scalars under a single "value" field so the test
		// fixture can still observe them when needed; preserves the
		// pre-redesign convenience without adding a field to the proto.
		return structpb.NewStruct(map[string]any{"value": fmt.Sprintf("%v", v)})
	}
	return structpb.NewStruct(m)
}
