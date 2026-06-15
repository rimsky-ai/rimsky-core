// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

// Command stubexecutor is a test-only Executor that returns Success for every
// dispatch. The integration harness builds it on demand (testcontainers
// FromDockerfile) and registers it as a peer executor so tests about stores,
// subscribers, and observability can complete the claim loop without a real
// executor. It is never published as a product image.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/ops"
)

// forcedErrorClass is the wire-level error_class the stub emits when
// EXECUTOR_STUB_FORCE_ERROR is set. It carries the `<executor>/<leaf>`
// hierarchical shape per `concept:signal`, mirroring how
// test/support/executors/stub prefixes single-segment classes with
// `stub/` — duplicated here because lib/services may not import the
// root module's test-support packages (consumption-side-isolation).
const forcedErrorClass = "stub/forced_error"

// server implements genv1.ExecutorServer. By default it sends a single
// terminal Success for every dispatch. When forceError is set (via
// EXECUTOR_STUB_FORCE_ERROR), it instead sends a single terminal Error
// with error_class=stub/forced_error — the abandon-case driver for the
// Gate-10 held-subgraph e2e, which needs the held co-holder set to
// aggregate to failure so auto-terminal fires Abandon (drop staging).
type server struct {
	genv1.UnimplementedExecutorServer
	forceError bool
}

// Execute emits exactly one terminal StreamClose event and closes the
// stream, per the Executor contract (zero heartbeats, no attribute
// writeback). The outcome is Success by default, or Error when
// forceError is set.
func (s server) Execute(_ *genv1.ExecuteRequest, stream genv1.Executor_ExecuteServer) error {
	if s.forceError {
		return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
			StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Error{Error: &genv1.Error{
				ErrorClass: forcedErrorClass,
			}}},
		}})
	}
	return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
		StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Success{Success: &genv1.Success{
			Changed:       false,
			ChangeSummary: "stub executor: success",
		}}},
	}})
}

// observability implements genv1.ExecutorObservabilityServer. The standalone
// stub keeps no traces, but it MUST answer Capabilities with a non-nil
// expected-attributes schema: the dispatch-time attribute-surface gate
// (runtime.resolveAttributes) rejects any attribute-bearing node whose
// executor advertises no schema with executor_schema_unavailable. Advertising
// the permissive open shape `{"type":"object"}` (no `properties` block, which
// graph/node.IsPermissiveExecutorSchema reads as "open shape") lets stub nodes
// that carry an `attributes:` block dispatch and settle instead of failing.
//
// This mirrors test/support/executors/stub's observability server, duplicated
// here because the lib/services module requires only lib/protocols and may not
// import the root module's test-support packages (consumption-side-isolation).
//
// When forceError is set, Capabilities additionally declares
// forcedErrorClass in DeclaredErrorClasses so a template node can route
// that class through an `error_types:` policy (give_up): the
// registration validator (graph/node template_validator) range-checks
// each error_types key against the executor's advertised vocabulary and
// rejects classes the executor does not declare. Declaring exactly the
// class the stub emits keeps the advertised vocabulary honest.
type observability struct {
	genv1.UnimplementedExecutorObservabilityServer
	forceError bool
}

// Capabilities reports the no-observability shape with a permissive
// expected-attributes schema. In forced-error mode it also advertises
// forcedErrorClass as a declared error class.
func (o observability) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	caps := &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              false,
		SupportsTraceStream:           false,
		RetentionAfterTerminalSeconds: 0,
		ExpectedAttributesSchema:      []byte(`{"type":"object"}`),
	}
	if o.forceError {
		caps.DeclaredErrorClasses = []string{forcedErrorClass}
	}
	return caps, nil
}

// GetTrace returns Unimplemented; the stub retains no traces.
func (observability) GetTrace(_ context.Context, _ *genv1.GetTraceRequest) (*genv1.Trace, error) {
	return nil, status.Error(codes.Unimplemented, "stub executor: GetTrace not supported")
}

// StreamTrace returns Unimplemented.
func (observability) StreamTrace(_ *genv1.StreamTraceRequest, _ genv1.ExecutorObservability_StreamTraceServer) error {
	return status.Error(codes.Unimplemented, "stub executor: StreamTrace not supported")
}

func main() {
	ops.Setup(slog.LevelInfo)
	bind := os.Getenv("EXECUTOR_STUB_BIND")
	if bind == "" {
		bind = "0.0.0.0:9300"
	}
	lis, err := net.Listen("tcp", bind)
	if err != nil {
		slog.Error("stubexecutor listen", "error", err.Error(), "bind", bind)
		os.Exit(1)
	}
	// @deliberate: EXECUTOR_STUB_FORCE_ERROR=1 flips the stub from
	// success-only to error-only; default (unset) preserves the
	// success-only behavior existing harness scenarios rely on.
	forceError := os.Getenv("EXECUTOR_STUB_FORCE_ERROR") == "1"
	srv := grpc.NewServer()
	genv1.RegisterExecutorServer(srv, server{forceError: forceError})
	genv1.RegisterExecutorObservabilityServer(srv, observability{forceError: forceError})
	slog.Info("stubexecutor listening", "bind", bind, "force_error", forceError)
	if err := srv.Serve(lis); err != nil {
		slog.Error("stubexecutor serve", "error", err.Error())
		os.Exit(1)
	}
}
