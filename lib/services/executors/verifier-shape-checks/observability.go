// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// observability.go implements the minimum ExecutorObservability surface
// for the verifier-shape-checks bundled executor: a Capabilities RPC
// that advertises the executor's hierarchical declared_error_classes
// per `concept:signal`. The executor has no in-process trace store, so
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

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/executors/verifier-shape-checks/errorclasses"
)

// ObservabilityServer is the verifier-shape-checks observability
// handler. No per-dispatch trace store; Capabilities is the only RPC
// the executor answers.
type ObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer
}

// NewObservabilityServer constructs the stateless capabilities handler.
func NewObservabilityServer() *ObservabilityServer { return &ObservabilityServer{} }

// Capabilities advertises the verifier-shape-checks hierarchical error
// vocabulary and a permissive-open attribute schema. The verifier
// expects `attributes.checks` (array) plus an optional `rows` payload
// but doesn't constrain other keys; we advertise the same
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
		// @constraint: The verifier ships the Validation mix-in
		// (validation.go) whose Validate handles role="executor" only.
		// Rimsky learns a validation peer's roles exclusively from this
		// handshake field — omitting it means the peer is dialed but
		// never selected by the registration-time pipeline (empty roles
		// never match role="executor").
		ValidationSupportedRoles: []string{"executor"},
	}, nil
}

// RegisterObservability registers the verifier-shape-checks
// observability server on srv. Returns the server for symmetry with
// http-node's helper (the verifier has no dispatch-time hooks to wire).
func RegisterObservability(srv *grpc.Server) *ObservabilityServer {
	o := NewObservabilityServer()
	genv1.RegisterExecutorObservabilityServer(srv, o)
	return o
}
