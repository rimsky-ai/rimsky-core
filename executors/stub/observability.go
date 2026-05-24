// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package stub

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

// ObservabilityServer is the capabilities-only ExecutorObservability
// implementation registered on the stub executor's gRPC server. The
// stub never retains traces; GetTrace and StreamTrace return
// Unimplemented. Used by conformance probes against executors that
// declare no observability surface.
type ObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer
}

// NewObservabilityServer returns the capabilities-only observability
// stub.
func NewObservabilityServer() *ObservabilityServer { return &ObservabilityServer{} }

// Capabilities reports the no-observability shape: every supports_*
// flag false, retention 0, no custom UI. DeclaredEvents lists the
// event names the stub emits in scenario fixtures so the F6 cross-
// validator accepts templates referencing them. The stub itself does
// not constrain emissions; this list mirrors the event names used
// across test/scenarios/.
func (*ObservabilityServer) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              false,
		SupportsTraceStream:           false,
		RetentionAfterTerminalSeconds: 0,
		// The stub executor accepts any attribute shape — declare an open
		// schema so the dispatch-time gate knows this is intentional rather
		// than a discovery cache miss. `{"type":"object"}` with no
		// `properties` block is recognised by
		// `graph/node.IsPermissiveExecutorSchema` as "open shape," so the
		// readOnly-fallback leg of the unified-attribute-surface check is
		// skipped.
		ExpectedAttributesSchema: []byte(`{"type":"object"}`),
		DeclaredEvents: []string{
			"ready",
			"signal",
			"checkpoint",
			"progress",
			"completed",
		},
		// 2026-05-23 signal-taxonomy Pass 6: the stub executor emits
		// scripted error classes for tests. Since the scripted vocabulary
		// is open-ended, advertise the `stub/*` prefix as a single
		// wildcard so operator templates' `error_types:` keys under the
		// `stub/` prefix (which the stub auto-prefixes at emit time per
		// `prefixedStubClass`) are accepted by the range-check at
		// registration.
		DeclaredErrorClasses: []string{"stub/*"},
	}, nil
}

// GetTrace returns Unimplemented; stub-mode probes accept this response.
func (*ObservabilityServer) GetTrace(_ context.Context, _ *genv1.GetTraceRequest) (*genv1.Trace, error) {
	return nil, status.Error(codes.Unimplemented, "stub executor: GetTrace not supported")
}

// StreamTrace returns Unimplemented.
func (*ObservabilityServer) StreamTrace(_ *genv1.StreamTraceRequest, _ genv1.ExecutorObservability_StreamTraceServer) error {
	return status.Error(codes.Unimplemented, "stub executor: StreamTrace not supported")
}

// RegisterObservability registers the stub observability server on srv
// alongside the existing Executor handler. Tests and the smoke
// fixture call this to expose the no-observability shape on the same
// listener as the dispatch surface.
func RegisterObservability(srv *grpc.Server) {
	genv1.RegisterExecutorObservabilityServer(srv, NewObservabilityServer())
}
