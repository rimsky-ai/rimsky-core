// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: Apache-2.0

package serverkit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

type stubObservabilityServer struct {
	genv1.UnimplementedClaimProducerObservabilityServer
	streamErr   error
	adminView   *genv1.AdminView
	lastParams  *structpb.Struct
	lastViewReq string
}

func (s *stubObservabilityServer) StreamClaim(_ *genv1.StreamClaimRequest, _ grpc.ServerStreamingServer[genv1.ClaimEvent]) error {
	return s.streamErr
}

func (s *stubObservabilityServer) GetAdminView(_ context.Context, req *genv1.GetAdminViewRequest) (*genv1.AdminView, error) {
	s.lastViewReq = req.GetViewName()
	s.lastParams = req.GetParams()
	if s.adminView == nil {
		return &genv1.AdminView{RenderHint: "table"}, nil
	}
	return s.adminView, nil
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

func TestObsAdminHandler_RejectsEmptyViewName(t *testing.T) {
	mux := http.NewServeMux()
	MountObservability(mux, &stubObservabilityServer{})
	req := httptest.NewRequest(http.MethodGet, "/observability/v1/admin/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty view_name: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestObsAdminHandler_RejectsNestedViewName(t *testing.T) {
	mux := http.NewServeMux()
	MountObservability(mux, &stubObservabilityServer{})
	req := httptest.NewRequest(http.MethodGet, "/observability/v1/admin/foo/bar", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("nested view_name: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestObsAdminHandler_ParsesViewNameAndQueryParams(t *testing.T) {
	srv := &stubObservabilityServer{}
	mux := http.NewServeMux()
	MountObservability(mux, srv)
	req := httptest.NewRequest(http.MethodGet, "/observability/v1/admin/backlog?state=open", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if srv.lastViewReq != "backlog" {
		t.Fatalf("view_name = %q, want %q", srv.lastViewReq, "backlog")
	}
	if got := srv.lastParams.GetFields()["state"].GetStringValue(); got != "open" {
		t.Fatalf("params[state] = %q, want %q", got, "open")
	}
}

func TestObsClaimsHandler_RejectsNestedClaimID(t *testing.T) {
	mux := http.NewServeMux()
	MountObservability(mux, &stubObservabilityServer{})
	req := httptest.NewRequest(http.MethodGet, "/observability/v1/claims/foo/bar", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("nested claim_id: status = %d, want %d", rec.Code, http.StatusBadRequest)
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
