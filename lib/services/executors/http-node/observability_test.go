// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package httpnode

import (
	"context"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestObservability_Capabilities(t *testing.T) {
	s := NewObservabilityServer("http://bridge.invalid")
	caps, err := s.Capabilities(context.Background(), &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.GetSupportsTraceGet() || !caps.GetSupportsTraceStream() {
		t.Fatalf("expected supports_trace_get + supports_trace_stream true; got %+v", caps)
	}
	if caps.GetRetentionAfterTerminalSeconds() != retentionSeconds {
		t.Fatalf("retention = %d, want %d", caps.GetRetentionAfterTerminalSeconds(), retentionSeconds)
	}
	if caps.GetHttpBridgeUrl() != "http://bridge.invalid" {
		t.Fatalf("http_bridge_url = %q, want the constructor-supplied URL", caps.GetHttpBridgeUrl())
	}
}

func TestObservability_DelegatesToSharedStore(t *testing.T) {
	s := NewObservabilityServer("")
	s.RegisterDispatch("d1")
	s.AppendEvent("d1", MakeEvent("e1", "", "step_started", "", genv1.Severity_INFO, map[string]any{"step_id": "s1"}))
	s.MarkTerminal("d1")
	tr, err := s.GetTrace(context.Background(), &genv1.GetTraceRequest{DispatchId: "d1"})
	if err != nil {
		t.Fatalf("GetTrace: %v", err)
	}
	if !tr.GetComplete() || len(tr.GetEvents()) != 1 {
		t.Fatalf("trace = %+v", tr)
	}
}
