// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package stub

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

// ObservabilityServer is the capabilities-only ExecutorObservability
// implementation registered on the stub executor's gRPC server. The
// stub never retains traces; GetTrace and StreamTrace return
// Unimplemented. Used by conformance probes against executors that
// declare no observability surface.
//
// ExpectedAttributesSchema is the JSON Schema the stub advertises via
// Capabilities. When empty, Capabilities falls back to the permissive
// `{"type":"object"}` shape (open schema). Tests that need a
// constraint-advertising executor (so that a genuinely-invalid
// reference can be made "invalid" — e.g. a `minimum:0` property) set a
// non-empty schema via NewObservabilityServerWithSchema.
type ObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer
	ExpectedAttributesSchema []byte
}

// permissiveSchema is the open-shape schema the stub advertises by
// default. `{"type":"object"}` with no `properties` block is
// recognised by graph/node.IsPermissiveExecutorSchema as "open shape,"
// so the readOnly-fallback leg of the unified-attribute-surface check
// is skipped.
var permissiveSchema = []byte(`{"type":"object"}`)

// NewObservabilityServer returns the capabilities-only observability
// stub advertising the permissive `{"type":"object"}` schema.
func NewObservabilityServer() *ObservabilityServer { return &ObservabilityServer{} }

// NewObservabilityServerWithSchema returns the capabilities-only
// observability stub advertising the supplied JSON Schema bytes. An
// empty/nil schema falls back to the permissive default at Capabilities
// time. Consumed by scenario tests standing up a constraint-advertising
// executor whose schema declares a violated property.
func NewObservabilityServerWithSchema(schema []byte) *ObservabilityServer {
	return &ObservabilityServer{ExpectedAttributesSchema: schema}
}

// Capabilities reports the no-observability shape: every supports_*
// flag false, retention 0, no custom UI. DeclaredEvents lists the
// event names the stub emits in scenario fixtures so the F6 cross-
// validator accepts templates referencing them. The stub itself does
// not constrain emissions; this list mirrors the event names used
// across test/scenarios/.
func (s *ObservabilityServer) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	// @deliberate: Advertise the configured schema when set, falling back to the
	// permissive open shape. The fallback keeps NewObservabilityServer()
	// (and every existing caller) back-compatible: an unconfigured stub
	// still declares `{"type":"object"}`.
	schema := s.ExpectedAttributesSchema
	if len(schema) == 0 {
		schema = permissiveSchema
	}
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              false,
		SupportsTraceStream:           false,
		RetentionAfterTerminalSeconds: 0,
		// @deliberate: The stub executor accepts any attribute shape by default —
		// declare an open schema so the dispatch-time gate knows this is
		// intentional rather than a discovery cache miss. A test may
		// override this with a constraining schema via
		// NewObservabilityServerWithSchema.
		ExpectedAttributesSchema: schema,
		DeclaredEvents: []string{
			"ready",
			"signal",
			"checkpoint",
			"progress",
			"completed",
		},
		// @deliberate: 2026-05-23 signal-taxonomy Pass 6: the stub executor emits
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
// alongside the existing Executor handler, advertising the permissive
// `{"type":"object"}` schema. Tests and the smoke fixture call this to
// expose the no-observability shape on the same listener as the
// dispatch surface.
func RegisterObservability(srv *grpc.Server) {
	genv1.RegisterExecutorObservabilityServer(srv, NewObservabilityServer())
}

// RegisterObservabilityWithSchema registers the stub observability
// server advertising the supplied JSON Schema bytes (empty → permissive
// default). Used to stand up a constraint-advertising executor on a
// dedicated listener so a genuinely-invalid reference can be exercised.
func RegisterObservabilityWithSchema(srv *grpc.Server, schema []byte) {
	genv1.RegisterExecutorObservabilityServer(srv, NewObservabilityServerWithSchema(schema))
}
