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

// GetCapabilities reports the no-observability shape: every supports_*
// flag false, retention 0, no custom UI.
func (*ObservabilityServer) GetCapabilities(_ context.Context, _ *genv1.GetCapabilitiesRequest) (*genv1.ObservabilityCapabilities, error) {
	return &genv1.ObservabilityCapabilities{
		SupportsTraceGet:              false,
		SupportsTraceStream:           false,
		RetentionAfterTerminalSeconds: 0,
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
// alongside the existing NodeExecutor handler. Tests and the smoke
// fixture call this to expose the no-observability shape on the same
// listener as the dispatch surface.
func RegisterObservability(srv *grpc.Server) {
	genv1.RegisterExecutorObservabilityServer(srv, NewObservabilityServer())
}
