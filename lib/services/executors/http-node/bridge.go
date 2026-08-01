// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package httpnode

import (
	"io"
	"net"
	"net/http"

	"google.golang.org/protobuf/encoding/protojson"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/internal/peerauth"
)

func NewBridgeServer(addr string, handler http.Handler, id *peerauth.Identity) *http.Server {
	srv := id.HTTPServer(handler)
	srv.Addr = addr
	return srv
}

func ServeBridge(srv *http.Server, lis net.Listener, id *peerauth.Identity) error {
	return id.RunHTTP(srv, lis)
}

const maxExecuteBodyBytes = 10 * 1024 * 1024

func MountBridge(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("/v1/Execute", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxExecuteBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req genv1.ExecuteRequest
		if err := protojson.Unmarshal(body, &req); err != nil {
			http.Error(w, "protojson: "+err.Error(), http.StatusBadRequest)
			return
		}
		outcome, err := s.Execute(r.Context(), &req)
		if err != nil {
			outcome = &genv1.Outcome{Outcome: &genv1.Outcome_Error{Error: &genv1.Error{
				ErrorClass: "http/internal_error",
			}}}
		}
		b, mErr := protojson.Marshal(outcome)
		if mErr != nil {
			http.Error(w, mErr.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	})
}
