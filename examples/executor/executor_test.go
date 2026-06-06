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

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// TestExecuteReturnsSingleSuccessTerminal starts the example executor in-process
// and asserts the dispatch protocol's happy path: the Execute stream yields
// exactly one StreamClose terminal carrying Success, then closes (io.EOF). It
// also asserts Capabilities advertises a non-empty attributes schema, without
// which attribute-bearing nodes would be rejected at dispatch.
func TestExecuteReturnsSingleSuccessTerminal(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	exec := &Executor{}
	genv1.RegisterExecutorServer(srv, exec)
	genv1.RegisterExecutorObservabilityServer(srv, exec)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

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
}
