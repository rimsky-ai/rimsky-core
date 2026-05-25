// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

package main

import (
	"io"
	"net/http"

	"google.golang.org/protobuf/encoding/protojson"

	genv1 "github.com/fallguyconsulting/rimsky/protocols/proto/v1/gen"
)

// mountBridge adds the HTTP+JSON bridge under /v1/Execute on the provided
// http.ServeMux. The bridge accepts a single protojson-encoded ExecuteRequest
// body and streams back protojson-encoded ExecuteEvents as ndjson (one JSON
// object per line).
//
// Design note: we use an ndjson sender closure rather than reimplementing the
// gRPC server stream interface — Server.executeCore is transport-independent
// and takes a sendFunc, so no stream-interface shim is required.
func mountBridge(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("/v1/Execute", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
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

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		send := func(e *genv1.ExecuteEvent) error {
			b, err := protojson.Marshal(e)
			if err != nil {
				return err
			}
			if _, err := w.Write(append(b, '\n')); err != nil {
				return err
			}
			if flusher != nil {
				flusher.Flush()
			}
			return nil
		}

		if err := s.executeCore(r.Context(), &req, send); err != nil {
			// Best-effort final errored event; the response may already be
			// partially written, so errors here are logged by callers and
			// otherwise ignored.
			b, _ := protojson.Marshal(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_StreamClose{
				StreamClose: &genv1.StreamClose{Outcome: &genv1.StreamClose_Error{Error: &genv1.Error{
					ErrorClass: "http/internal_error",
				}}},
			}})
			_, _ = w.Write(append(b, '\n'))
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
}
