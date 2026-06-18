// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
)

func mountObservabilityBridge(mux *http.ServeMux, obs *ObservabilityServer, httpBridgeURL string) {
	mux.HandleFunc("/observability/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		caps := &genv1.ObservabilityCapabilities{
			SupportsTraceGet:              true,
			SupportsTraceStream:           true,
			RetentionAfterTerminalSeconds: retentionSeconds,
			HttpBridgeUrl:                 httpBridgeURL,
			ExpectedAttributesSchema:      []byte(`{"type":"object"}`),
		}
		writeProtoJSON(w, caps)
	})

	mux.HandleFunc("/observability/v1/trace/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/observability/v1/trace/")
		isStream := strings.HasSuffix(path, "/stream")
		dispatchID := strings.TrimSuffix(path, "/stream")
		if dispatchID == "" || strings.Contains(dispatchID, "/") {
			http.Error(w, "bad dispatch_id", http.StatusBadRequest)
			return
		}
		if isStream {
			handleTraceStreamHTTP(w, r, obs, dispatchID)
			return
		}
		trace, err := obs.GetTrace(r.Context(), &genv1.GetTraceRequest{DispatchId: dispatchID})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeProtoJSON(w, trace)
	})
}

func handleTraceStreamHTTP(w http.ResponseWriter, r *http.Request, obs *ObservabilityServer, dispatchID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	errClientGone := errors.New("sse: client gone")
	send := func(ev *genv1.TraceEvent) error {
		b, _ := protojson.Marshal(ev)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return errClientGone
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}
	sub, exists := obs.subscribe(dispatchID)
	if !exists {
		_ = send(traceCompleteEvent())
		return
	}
	defer obs.unsubscribe(dispatchID, sub)
	cursor := 0
	idle := defaultStreamIdleTimeout
	for {
		events, terminal := obs.drainFrom(dispatchID, cursor)
		cursor += len(events)
		for _, ev := range events {
			if err := send(ev); err != nil {
				return
			}
		}
		if terminal {
			_ = send(traceCompleteEvent())
			return
		}
		var idleC <-chan time.Time
		if idle > 0 {
			t := time.NewTimer(idle)
			idleC = t.C
			defer t.Stop()
		}
		select {
		case <-r.Context().Done():
			return
		case <-sub.done:
			tail, _ := obs.drainFrom(dispatchID, cursor)
			for _, ev := range tail {
				if err := send(ev); err != nil {
					return
				}
			}
			_ = send(traceCompleteEvent())
			return
		case <-idleC:
			_ = send(traceCompleteEvent())
			return
		case <-sub.wake:
		}
	}
}

func writeProtoJSON(w http.ResponseWriter, m proto.Message) {
	w.Header().Set("Content-Type", "application/json")
	b, err := protojson.Marshal(m)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(b)
}
