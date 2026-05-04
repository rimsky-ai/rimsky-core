package main

import (
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	genv1 "github.com/fallguy/rimsky/proto/v1/gen"
)

// mountObservabilityBridge wires the HTTP+JSON observability routes
// onto the executor's existing /v1/* HTTP listener. Routes:
//
//	GET /observability/v1/capabilities
//	GET /observability/v1/trace/{dispatch_id}
//	GET /observability/v1/trace/{dispatch_id}/stream  (SSE)
//
// Per spec §2.1.
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

// handleTraceStreamHTTP serves the SSE form of StreamTrace. Each event
// is emitted as one `data:` line of JSON. Unknown dispatches close
// immediately with the same evicted shape GetTrace returns (per spec
// §2.6).
//
// The subscriber-pump model in ObservabilityServer.subscribe means a
// new subscriber registers atomically under the lock, then reads
// events out of the per-dispatch slice at its own cursor on each
// wakeup — so AppendEvent calls that land between snapshot capture
// and the first read still surface in the next drain pass. Spec §2.6
// requires no events dropped under append concurrency.
func handleTraceStreamHTTP(w http.ResponseWriter, r *http.Request, obs *ObservabilityServer, dispatchID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)
	send := func(ev *genv1.TraceEvent) {
		b, _ := protojson.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	sub, exists := obs.subscribe(dispatchID)
	if !exists {
		send(traceCompleteEvent())
		return
	}
	defer obs.unsubscribe(dispatchID, sub)
	cursor := 0
	for {
		events, terminal := obs.drainFrom(dispatchID, cursor)
		cursor += len(events)
		for _, ev := range events {
			send(ev)
		}
		if terminal {
			send(traceCompleteEvent())
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-sub.done:
			tail, _ := obs.drainFrom(dispatchID, cursor)
			for _, ev := range tail {
				send(ev)
			}
			send(traceCompleteEvent())
			return
		case <-sub.wake:
			// Loop back and drain.
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
