// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Package stubtest provides test-only helpers around executors/stub.
// Keeping the testing.TB-dependent helpers here lets the stub package
// itself stay free of test-only dependencies.
package stubtest

import (
	"net"
	"testing"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/executors/stub"
)

// Listen starts a gRPC server on an OS-assigned port and registers s as
// the Executor handler plus the capabilities-only
// ExecutorObservability surface advertising the permissive
// `{"type":"object"}` schema. Registers cleanup via t.Cleanup to stop
// the server. Returns the server and its listening address.
func Listen(t testing.TB, s *stub.Stub) (*grpc.Server, string) {
	return ListenWithSchema(t, s, nil)
}

// ListenWithSchema is Listen but advertises the supplied
// expected_attributes_schema bytes via the observability Capabilities
// surface (empty/nil → permissive default). Used to stand up a
// constraint-advertising stub executor whose schema declares a
// property a reference can violate.
func ListenWithSchema(t testing.TB, s *stub.Stub, schema []byte) (*grpc.Server, string) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, s)
	stub.RegisterObservabilityWithSchema(srv, schema)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop() })
	return srv, lis.Addr().String()
}
