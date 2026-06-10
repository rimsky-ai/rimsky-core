// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// TestExecuteReturnsSingleSuccessTerminal starts the example executor in-process
// and asserts the dispatch protocol's happy path: the Execute stream yields
// exactly one StreamClose terminal carrying Success, then closes (io.EOF). It
// also asserts Capabilities advertises a non-empty attributes schema, a
// non-empty declared events list, and a non-empty declared error classes
// list — the three handshake fields rimsky reads at startup and gates
// templates against.
func TestExecuteReturnsSingleSuccessTerminal(t *testing.T) {
	conn := startInProcessExecutor(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := genv1.NewExecutorClient(conn).Execute(ctx, &genv1.ExecuteRequest{
		NodeId: "n1", InstanceId: "i1", NodeType: "example",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	terminals := 0
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if sc := ev.GetStreamClose(); sc != nil {
			terminals++
			if sc.GetSuccess() == nil {
				t.Fatalf("terminal outcome is not Success: %+v", sc.GetOutcome())
			}
		}
	}
	if terminals != 1 {
		t.Fatalf("want exactly one StreamClose terminal, got %d", terminals)
	}

	caps, err := genv1.NewExecutorObservabilityClient(conn).
		Capabilities(ctx, &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if len(caps.GetExpectedAttributesSchema()) == 0 {
		t.Fatal("Capabilities advertised no attributes schema; attribute-bearing nodes would be rejected")
	}
	if len(caps.GetDeclaredEvents()) == 0 {
		t.Fatal("Capabilities advertised no declared_events; subscriptions to event/<name> would be refused at registration")
	}
	if len(caps.GetDeclaredErrorClasses()) == 0 {
		t.Fatal("Capabilities advertised no declared_error_classes; operator error_types: policy keys would be refused at registration")
	}
}

// TestExecute_RaiseErrorEmitsDeclaredClass asserts that when mode is
// `raise_error`, the StreamClose terminal carries Error with the
// DeclaredErrorClass — the same string the operator's `error_types:`
// chain keys on. Pinning this in-process catches a regression in the
// executor's class spelling without the cross-stack proof's overhead.
func TestExecute_RaiseErrorEmitsDeclaredClass(t *testing.T) {
	conn := startInProcessExecutor(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	attrs, err := structpb.NewStruct(map[string]any{"mode": "raise_error"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}

	stream, err := genv1.NewExecutorClient(conn).Execute(ctx, &genv1.ExecuteRequest{
		NodeId: "n1", InstanceId: "i1", NodeType: "example", Attributes: attrs,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var gotClass string
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if sc := ev.GetStreamClose(); sc != nil {
			if e := sc.GetError(); e != nil {
				gotClass = e.GetErrorClass()
			}
		}
	}
	if gotClass != DeclaredErrorClass {
		t.Fatalf("error_class on the terminal: got %q want %q (the operator's error_types: chain keys on this exact string)",
			gotClass, DeclaredErrorClass)
	}
}

// TestExecute_EmitEventEmitsDeclaredName asserts that when mode is
// `emit_event`, the stream emits at least one NamedEvent whose name is
// DeclaredEventName before the Success terminal. Pinning this in-process
// catches a regression in the event name spelling without the cross-stack
// proof's overhead — the cross-stack proof then asserts the named event
// appears as a kind=`event/<name>` row on GET /v1/events.
func TestExecute_EmitEventEmitsDeclaredName(t *testing.T) {
	conn := startInProcessExecutor(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	attrs, err := structpb.NewStruct(map[string]any{"mode": "emit_event"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}

	stream, err := genv1.NewExecutorClient(conn).Execute(ctx, &genv1.ExecuteRequest{
		NodeId: "n1", InstanceId: "i1", NodeType: "example", Attributes: attrs,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var (
		sawDeclared    bool
		successOutcome bool
	)
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		if ne := ev.GetNamedEvent(); ne != nil {
			if ne.GetName() == DeclaredEventName {
				sawDeclared = true
			}
		}
		if sc := ev.GetStreamClose(); sc != nil {
			successOutcome = sc.GetSuccess() != nil
		}
	}
	if !sawDeclared {
		t.Fatalf("did not see a NamedEvent with name %q on the stream", DeclaredEventName)
	}
	if !successOutcome {
		t.Fatal("terminal outcome on emit_event mode must be Success (event-emit-then-success contract)")
	}
}

// startInProcessExecutor stands up the Executor as an in-process gRPC
// server on a free port and returns a connected client conn. Cleanup is
// registered via t.Cleanup; callers do not need to defer anything.
func startInProcessExecutor(t *testing.T) *grpc.ClientConn {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	exec := &Executor{}
	genv1.RegisterExecutorServer(srv, exec)
	genv1.RegisterExecutorObservabilityServer(srv, exec)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
