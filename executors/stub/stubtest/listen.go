// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

// Package stubtest provides test-only helpers around executors/stub. The
// production stub binary (executors/stub/cmd) does not need testing.TB;
// scenario tests do.
package stubtest

import (
	"net"
	"testing"

	"google.golang.org/grpc"

	"github.com/rimsky-ai/rimsky-core/executors/stub"
	genv1 "github.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen"
)

// Listen starts a gRPC server on an OS-assigned port and registers s as
// the Executor handler plus the capabilities-only
// ExecutorObservability surface. Registers cleanup via t.Cleanup to stop
// the server. Returns the server and its listening address.
func Listen(t testing.TB, s *stub.Stub) (*grpc.Server, string) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, s)
	stub.RegisterObservability(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })
	return srv, lis.Addr().String()
}
