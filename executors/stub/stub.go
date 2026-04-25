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
	mu      sync.Mutex
	scripts map[string]*script
}

// New constructs a Stub with no scripted node types registered.
func New() *Stub { return &Stub{scripts: map[string]*script{}} }

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
func (s *Stub) Execute(req *genv1.ExecuteRequest, stream genv1.NodeExecutor_ExecuteServer) error {
	s.mu.Lock()
	sc, ok := s.scripts[req.NodeType]
	s.mu.Unlock()
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
		v, err := toValue(sc.result)
		if err != nil {
			return err
		}
		return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Complete{Complete: &genv1.Complete{
			Result: v, Changed: sc.changed, ChangeSummary: sc.changeSum,
		}}})
	case termError:
		v, err := toValue(sc.payload)
		if err != nil {
			return err
		}
		return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Errored{Errored: &genv1.Errored{
			ErrorClass: sc.errorClass, Payload: v,
		}}})
	case termBlocked:
		v, err := toValue(sc.payload)
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

func toValue(v any) (*structpb.Value, error) {
	if v == nil {
		return structpb.NewNullValue(), nil
	}
	return structpb.NewValue(v)
}
