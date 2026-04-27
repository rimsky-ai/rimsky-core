// Package stub is a scripted in-process executor used by scenario tests.
// NOT a user-facing reference executor — lives under executors/stub/ so
// tests can drive any sequence of Execute events against a real gRPC server.
package stub

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

type terminalKind int

const (
	termComplete terminalKind = iota
	termError
	termBlocked
	termAsync
)

type script struct {
	terminal          terminalKind
	result            any
	changed           bool
	changeSum         string
	errorClass        string
	payload           any
	reason            string
	asyncAckID        string
	asyncCompletionMs int64
	heartbeats        int
	delay             time.Duration
}

// Stub is a scripted NodeExecutor server for tests.
type Stub struct {
	genv1.UnimplementedNodeExecutorServer
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

// EnableStubMode switches the Stub into immediate-Complete mode. In this mode
// every Execute call short-circuits scripted behavior and returns a single
// Complete event with `changed: true` and `attributes_delta` populated from
// StubAttributesFor(node_type). Used by conformance probes and end-to-end
// stack tests where the executor surface is exercised but not the application
// logic.
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
// terminal is a Complete with changed=true.
func (s *Stub) WhenType(t string) *TypeBuilder {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc := &script{terminal: termComplete, changed: true}
	s.scripts[t] = sc
	return &TypeBuilder{s: s, typ: t}
}

// Complete configures the scripted terminal as a Complete event.
func (b *TypeBuilder) Complete(result any, changed bool, changeSummary string) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.s.scripts[b.typ]
	sc.terminal, sc.result, sc.changed, sc.changeSum = termComplete, result, changed, changeSummary
	return b
}

// Error configures the scripted terminal as an Errored event.
func (b *TypeBuilder) Error(class string, payload any) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.s.scripts[b.typ]
	sc.terminal, sc.errorClass, sc.payload = termError, class, payload
	return b
}

// Blocked configures the scripted terminal as a Blocked event.
func (b *TypeBuilder) Blocked(reason string, ctxv any) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.s.scripts[b.typ]
	sc.terminal, sc.reason, sc.payload = termBlocked, reason, ctxv
	return b
}

// AsyncAccepted configures the scripted terminal as an AsyncAccepted event.
func (b *TypeBuilder) AsyncAccepted(ackID string, completionMs int64) *TypeBuilder {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	sc := b.s.scripts[b.typ]
	sc.terminal, sc.asyncAckID, sc.asyncCompletionMs = termAsync, ackID, completionMs
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

// Execute implements genv1.NodeExecutorServer by streaming scripted events.
// Records the incoming request (id/type/attributes/userdata) for test
// inspection. If stub mode is enabled, short-circuits to an immediate
// Complete event keyed by node_type via StubAttributesFor.
func (s *Stub) Execute(req *genv1.ExecuteRequest, stream genv1.NodeExecutor_ExecuteServer) error {
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
		return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Complete{Complete: &genv1.Complete{
			AttributesDelta: delta,
			Changed:         true,
			ChangeSummary:   "stub",
		}}})
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

	// terminal
	switch sc.terminal {
	case termComplete:
		// Spec §12.2: Complete now carries `attributes_delta` (a Struct)
		// instead of the legacy `result` field. The stub's `result` API
		// is preserved for test convenience and mapped to an
		// AttributesDelta map when the value is a map[string]any (the
		// only realistic shape post-redesign — the supervisor merges it
		// into the resolved attribute object). Non-map / nil values are
		// dropped; the executor side has no other field to emit them on.
		delta, err := toStruct(sc.result)
		if err != nil {
			return err
		}
		return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Complete{Complete: &genv1.Complete{
			AttributesDelta: delta, Changed: sc.changed, ChangeSummary: sc.changeSum,
		}}})
	case termError:
		v, err := toStruct(sc.payload)
		if err != nil {
			return err
		}
		return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Errored{Errored: &genv1.Errored{
			ErrorClass: sc.errorClass, Payload: v,
		}}})
	case termBlocked:
		v, err := toStruct(sc.payload)
		if err != nil {
			return err
		}
		return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Blocked{Blocked: &genv1.Blocked{
			Reason: sc.reason, Context: v,
		}}})
	case termAsync:
		return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_AsyncAccepted{AsyncAccepted: &genv1.AsyncAccepted{
			AsyncAckId: sc.asyncAckID, ExpectedCompletionMs: sc.asyncCompletionMs,
		}}})
	}
	return nil
}

// Listen starts a gRPC server on an OS-assigned port and registers the Stub
// as the NodeExecutor handler. Registers cleanup via t.Cleanup to stop the
// server. Returns the server and its listening address.
func (s *Stub) Listen(t testing.TB) (*grpc.Server, string) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	genv1.RegisterNodeExecutorServer(srv, s)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })
	return srv, lis.Addr().String()
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
// AttributesDelta / Errored.Payload / Blocked.Context. Nil inputs return
// nil — the supervisor treats nil delta as "no writeback".
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
