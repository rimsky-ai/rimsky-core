// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package serverkit

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func MountObservability(mux *http.ServeMux, srv genv1.ClaimProducerObservabilityServer) {
	mux.HandleFunc("/observability/v1/capabilities", obsCapabilitiesHandler(srv))
	mux.HandleFunc("/observability/v1/claims", obsListClaimsHandler(srv))
	mux.HandleFunc("/observability/v1/claims/", obsClaimsHandler(srv))
	mux.HandleFunc("/observability/v1/admin/", obsAdminHandler(srv))
}

func obsCapabilitiesHandler(srv genv1.ClaimProducerObservabilityServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp, err := srv.Capabilities(r.Context(), &genv1.GetClaimProducerCapabilitiesRequest{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeProtoJSONResp(w, resp)
	}
}

func obsListClaimsHandler(srv genv1.ClaimProducerObservabilityServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/observability/v1/claims" {
			http.NotFound(w, r)
			return
		}
		req := &genv1.ListClaimsRequest{
			StateFilter: r.URL.Query().Get("state_filter"),
			Cursor:      r.URL.Query().Get("cursor"),
		}
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				http.Error(w, "bad limit", http.StatusBadRequest)
				return
			}
			req.Limit = uint32(n)
		}
		resp, err := srv.ListClaims(r.Context(), req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeProtoJSONResp(w, resp)
	}
}

func obsClaimsHandler(srv genv1.ClaimProducerObservabilityServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/observability/v1/claims/")
		isStream := strings.HasSuffix(path, "/stream")
		claimID := strings.TrimSuffix(path, "/stream")
		if claimID == "" || strings.Contains(claimID, "/") {
			http.Error(w, "bad claim_id", http.StatusBadRequest)
			return
		}
		if isStream {
			handleClaimStreamHTTP(w, r, srv, claimID)
			return
		}
		resp, err := srv.GetClaim(r.Context(), &genv1.GetClaimRequest{ClaimId: claimID})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeProtoJSONResp(w, resp)
	}
}

func obsAdminHandler(srv genv1.ClaimProducerObservabilityServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		viewName := strings.TrimPrefix(r.URL.Path, "/observability/v1/admin/")
		if viewName == "" || strings.Contains(viewName, "/") {
			http.Error(w, "bad view_name", http.StatusBadRequest)
			return
		}
		params := map[string]any{}
		for k := range r.URL.Query() {
			params[k] = r.URL.Query().Get(k)
		}
		paramStruct, err := structpb.NewStruct(params)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp, err := srv.GetAdminView(r.Context(), &genv1.GetAdminViewRequest{
			ViewName: viewName,
			Params:   paramStruct,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeProtoJSONResp(w, resp)
	}
}

func handleClaimStreamHTTP(w http.ResponseWriter, r *http.Request, srv genv1.ClaimProducerObservabilityServer, claimID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	stream := &sseClaimStream{w: w, ctx: r.Context(), flusher: flusher}
	if err := srv.StreamClaim(&genv1.StreamClaimRequest{ClaimId: claimID}, stream); err != nil {
		if _, werr := fmt.Fprintf(w, "data: {\"error\":%q}\n\n", err.Error()); werr == nil {
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

type sseClaimStream struct {
	w       http.ResponseWriter
	ctx     context.Context
	flusher http.Flusher
}

func (s *sseClaimStream) Send(ev *genv1.ClaimEvent) error {
	b, err := protojson.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", b); err != nil {
		return fmt.Errorf("sseClaimStream.Send: %w", err)
	}
	if s.flusher != nil {
		s.flusher.Flush()
	}
	return nil
}

func (s *sseClaimStream) Context() context.Context       { return s.ctx }
func (s *sseClaimStream) SendMsg(m any) error            { return nil }
func (s *sseClaimStream) RecvMsg(m any) error            { return nil }
func (s *sseClaimStream) SendHeader(_ metadata.MD) error { return nil }
func (s *sseClaimStream) SetHeader(_ metadata.MD) error  { return nil }
func (s *sseClaimStream) SetTrailer(_ metadata.MD)       {}

func writeProtoJSONResp(w http.ResponseWriter, m proto.Message) {
	w.Header().Set("Content-Type", "application/json")
	b, err := protojson.Marshal(m)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(b)
}
