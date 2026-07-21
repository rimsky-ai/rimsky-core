// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package httpnode

import (
	"context"
	"time"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/executors/internal/observability"
)

const retentionSeconds = observability.RetentionSeconds

type ObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer

	store         *observability.Store
	httpBridgeURL string
}

func NewObservabilityServer(httpBridgeURL string) *ObservabilityServer {
	return &ObservabilityServer{store: observability.NewStore(), httpBridgeURL: httpBridgeURL}
}

func (s *ObservabilityServer) Store() *observability.Store { return s.store }

func (s *ObservabilityServer) SetIdleTimeout(d time.Duration) { s.store.SetIdleTimeout(d) }

func (s *ObservabilityServer) RegisterDispatch(nodeRunID string) {
	s.store.RegisterDispatch(nodeRunID)
}

func (s *ObservabilityServer) AppendEvent(nodeRunID string, ev *genv1.TraceEvent) {
	s.store.AppendEvent(nodeRunID, ev)
}

func (s *ObservabilityServer) MarkTerminal(nodeRunID string) { s.store.MarkTerminal(nodeRunID) }

func (s *ObservabilityServer) SweepEvicted(now time.Time) { s.store.SweepEvicted(now) }

func (s *ObservabilityServer) CapabilitiesPayload() *genv1.ObservabilityCapabilities {
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              true,
		SupportsTraceStream:           true,
		RetentionAfterTerminalSeconds: retentionSeconds,
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

func MakeEvent(eventID, parentID, category, message string, sev genv1.Severity, attrs map[string]any) *genv1.TraceEvent {
	return observability.MakeEvent(eventID, parentID, category, message, sev, attrs)
}

func RegisterObservability(srv *grpc.Server, httpBridgeURL string) *ObservabilityServer {
	o := NewObservabilityServer(httpBridgeURL)
	genv1.RegisterExecutorObservabilityServer(srv, o)
	return o
}
