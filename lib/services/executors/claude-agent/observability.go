// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package claudeagent

import (
	"context"
	"time"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/executors/internal/observability"
)

type ObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer

	store         *observability.Store
	httpBridgeURL string
}

func NewObservabilityServer(httpBridgeURL string) *ObservabilityServer {
	return &ObservabilityServer{
		store:         observability.NewStore(),
		httpBridgeURL: httpBridgeURL,
	}
}

func (s *ObservabilityServer) Store() *observability.Store { return s.store }

func (s *ObservabilityServer) RegisterDispatch(dispatchID string) {
	s.store.RegisterDispatch(dispatchID)
}

func (s *ObservabilityServer) AppendEvent(dispatchID string, ev *genv1.TraceEvent) {
	s.store.AppendEvent(dispatchID, ev)
}

func (s *ObservabilityServer) MarkTerminal(dispatchID string) { s.store.MarkTerminal(dispatchID) }

func (s *ObservabilityServer) SweepEvicted(now time.Time) { s.store.SweepEvicted(now) }

func (s *ObservabilityServer) CapabilitiesPayload() *genv1.ObservabilityCapabilities {
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              true,
		SupportsTraceStream:           true,
		RetentionAfterTerminalSeconds: observability.RetentionSeconds,
		HttpBridgeUrl:                 s.httpBridgeURL,
		ExpectedAttributesSchema:      SchemaBytes(),
		DeclaredTags:                  DeclaredTags(),
		DeclaredErrorClasses:          DeclaredErrorClasses(),
	}
}

func (s *ObservabilityServer) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return s.CapabilitiesPayload(), nil
}

func (s *ObservabilityServer) GetTrace(ctx context.Context, req *genv1.GetTraceRequest) (*genv1.Trace, error) {
	return s.store.GetTrace(ctx, req)
}

func (s *ObservabilityServer) StreamTrace(req *genv1.StreamTraceRequest, stream genv1.ExecutorObservability_StreamTraceServer) error {
	return s.store.StreamTrace(req, stream)
}
