// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package stubtest

import (
	"net"
	"testing"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/test/support/executors/stub"
)

func Listen(t testing.TB, s *stub.Stub) (*grpc.Server, string) {
	return ListenWithSchema(t, s, nil)
}

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
