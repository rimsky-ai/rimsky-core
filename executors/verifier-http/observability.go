// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

// observability.go implements the minimum ExecutorObservability surface
// for the verifier-http bundled executor: a Capabilities RPC that
// advertises the executor's hierarchical declared_error_classes per
// `concept:signal`. verifier-http has no in-process trace store, so
// SupportsTraceGet / SupportsTraceStream stay false.
//
// Pattern mirrors `code:executors/http-node/observability.go::Capabilities`
// (the bundled-executor reference) — kept minimal because the verifier
// has nothing to surface beyond the error vocabulary and the
// permissive-open attribute schema.

package main

import (
	"context"

	"google.golang.org/grpc"

	"github.com/fallguyconsulting/rimsky/executors/verifier-http/errorclasses"
	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

// ObservabilityServer is the verifier-http observability handler. No
// per-dispatch trace store; Capabilities is the only RPC the executor
// answers.
type ObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer
}

// NewObservabilityServer constructs the stateless capabilities handler.
func NewObservabilityServer() *ObservabilityServer { return &ObservabilityServer{} }

// Capabilities advertises the verifier-http hierarchical error
// vocabulary and a permissive-open attribute schema. The verifier
// reads a small fixed set of attribute keys (`url`, `body`,
// `expected_status`, `timeout_ms`, `stub_probe`) but does not constrain
// the caller's shape beyond that, so we advertise the same
// `{"type":"object"}` open shape http-node uses — recognised by
// `graph/node.IsPermissiveExecutorSchema` as "open shape" so the
// dispatch-time `executor_schema_unavailable` gate doesn't fire a
// readOnly-fallback leg against the verifier.
func (s *ObservabilityServer) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:         false,
		SupportsTraceStream:      false,
		ExpectedAttributesSchema: []byte(`{"type":"object"}`),
		DeclaredErrorClasses:     errorclasses.Declared(),
	}, nil
}

// RegisterObservability registers the verifier-http observability
// server on srv. Returns the server for symmetry with http-node's
// helper (the verifier has no dispatch-time hooks to wire).
func RegisterObservability(srv *grpc.Server) *ObservabilityServer {
	o := NewObservabilityServer()
	genv1.RegisterExecutorObservabilityServer(srv, o)
	return o
}
