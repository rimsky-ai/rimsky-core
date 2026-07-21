// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package httpnode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func TestMountObservabilityBridge_HTTPCapabilitiesMatchesGRPC(t *testing.T) {
	obs := NewObservabilityServer("http://bridge.invalid")
	mux := http.NewServeMux()
	MountObservabilityBridge(mux, obs)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/observability/v1/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var httpCaps struct {
		DeclaredTags         []string `json:"declaredTags"`
		DeclaredErrorClasses []string `json:"declaredErrorClasses"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&httpCaps); err != nil {
		t.Fatal(err)
	}

	grpcCaps, err := obs.Capabilities(context.Background(), &genv1.ExecutorCapabilitiesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(httpCaps.DeclaredErrorClasses) != len(grpcCaps.GetDeclaredErrorClasses()) {
		t.Fatalf("HTTP capabilities declared_error_classes = %v, want parity with gRPC %v", httpCaps.DeclaredErrorClasses, grpcCaps.GetDeclaredErrorClasses())
	}
	if len(httpCaps.DeclaredErrorClasses) == 0 {
		t.Fatal("expected the HTTP capabilities endpoint to carry declared_error_classes, matching the gRPC Capabilities RPC")
	}
}
