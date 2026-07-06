// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package httpnode

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/executors/internal/observability"
)

const retentionSeconds = observability.RetentionSeconds

type ObservabilityServer struct {
	genv1.UnimplementedExecutorObservabilityServer

	store             *observability.Store
	httpBridgeURLOnce sync.Once
	httpBridgeURLMu   sync.RWMutex
	httpBridgeURL     string
}

func NewObservabilityServer() *ObservabilityServer {
	return &ObservabilityServer{store: observability.NewStore()}
}

func (s *ObservabilityServer) Store() *observability.Store { return s.store }

func (s *ObservabilityServer) SetHTTPBridgeURL(u string) {
	s.httpBridgeURLOnce.Do(func() {
		s.httpBridgeURLMu.Lock()
		defer s.httpBridgeURLMu.Unlock()
		s.httpBridgeURL = u
	})
}

func (s *ObservabilityServer) SetIdleTimeout(d time.Duration) { s.store.SetIdleTimeout(d) }

func (s *ObservabilityServer) RegisterDispatch(nodeRunID string) {
	s.store.RegisterDispatch(nodeRunID)
}

func (s *ObservabilityServer) AppendEvent(nodeRunID string, ev *genv1.TraceEvent) {
	s.store.AppendEvent(nodeRunID, ev)
}

func (s *ObservabilityServer) MarkTerminal(nodeRunID string) { s.store.MarkTerminal(nodeRunID) }

func (s *ObservabilityServer) SweepEvicted(now time.Time) { s.store.SweepEvicted(now) }

func (s *ObservabilityServer) Capabilities(_ context.Context, _ *genv1.ExecutorCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	s.httpBridgeURLMu.RLock()
	url := s.httpBridgeURL
	s.httpBridgeURLMu.RUnlock()
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              true,
		SupportsTraceStream:           true,
		RetentionAfterTerminalSeconds: retentionSeconds,
		HttpBridgeUrl:                 url,
		ExpectedAttributesSchema:      SchemaBytes(),
		DeclaredTags:                  DeclaredTags(),
		DeclaredErrorClasses:          DeclaredErrorClasses(),
	}, nil
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

func RegisterObservability(srv *grpc.Server) *ObservabilityServer {
	o := NewObservabilityServer()
	genv1.RegisterExecutorObservabilityServer(srv, o)
	return o
}
