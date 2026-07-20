// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package serverkit

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type stubObservabilityServer struct {
	genv1.UnimplementedClaimProducerObservabilityServer
	streamErr error
}

func (s *stubObservabilityServer) StreamClaim(_ *genv1.StreamClaimRequest, _ grpc.ServerStreamingServer[genv1.ClaimEvent]) error {
	return s.streamErr
}

func TestObsListClaimsHandler_RejectsNegativeLimit(t *testing.T) {
	mux := http.NewServeMux()
	MountObservability(mux, &stubObservabilityServer{})
	req := httptest.NewRequest(http.MethodGet, "/observability/v1/claims?limit=-1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("limit=-1: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestObsListClaimsHandler_RejectsOverflowingLimit(t *testing.T) {
	mux := http.NewServeMux()
	MountObservability(mux, &stubObservabilityServer{})
	req := httptest.NewRequest(http.MethodGet, "/observability/v1/claims?limit=99999999999", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("limit=99999999999: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleClaimStreamHTTP_EncodesErrorAsValidJSON(t *testing.T) {
	streamErr := errors.New("bad byte follows: \x1b[31m escape")
	req := httptest.NewRequest(http.MethodGet, "/observability/v1/claims/c1/stream", nil)
	rec := httptest.NewRecorder()
	handleClaimStreamHTTP(rec, req, &stubObservabilityServer{streamErr: streamErr}, "c1")

	body := rec.Body.String()
	const prefix = "data: "
	const suffix = "\n\n"
	if len(body) < len(prefix)+len(suffix) || body[:len(prefix)] != prefix {
		t.Fatalf("SSE body malformed: %q", body)
	}
	payload := body[len(prefix) : len(body)-len(suffix)]
	var decoded map[string]string
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("SSE data payload is not valid JSON: %v; payload=%q", err, payload)
	}
	if decoded["error"] != streamErr.Error() {
		t.Fatalf("decoded error = %q, want %q", decoded["error"], streamErr.Error())
	}
}
