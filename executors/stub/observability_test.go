package stub

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	genv1 "github.com/fallguy/rimsky/protocols/proto/v1/gen"
)

func TestObservability_Capabilities_NoObservability(t *testing.T) {
	s := NewObservabilityServer()
	caps, err := s.GetCapabilities(context.Background(), &genv1.GetCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	if caps.GetSupportsTraceGet() {
		t.Errorf("supports_trace_get = true, want false")
	}
	if caps.GetSupportsTraceStream() {
		t.Errorf("supports_trace_stream = true, want false")
	}
	if caps.GetRetentionAfterTerminalSeconds() != 0 {
		t.Errorf("retention = %d, want 0", caps.GetRetentionAfterTerminalSeconds())
	}
	if caps.GetCustomUi() != nil && caps.GetCustomUi().GetUiUrl() != "" {
		t.Errorf("custom_ui set; want nil")
	}
}

func TestObservability_GetTrace_Unimplemented(t *testing.T) {
	s := NewObservabilityServer()
	_, err := s.GetTrace(context.Background(), &genv1.GetTraceRequest{DispatchId: "x"})
	st, _ := status.FromError(err)
	if st.Code() != codes.Unimplemented {
		t.Fatalf("err code = %v, want Unimplemented", st.Code())
	}
}
