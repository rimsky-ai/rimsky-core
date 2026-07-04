// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package httpnode

import (
	"net/http"

	genv1 "github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen"
	"github.com/rimsky-ai/rimsky-core/lib/services/executors/internal/observability"
)

func MountObservabilityBridge(mux *http.ServeMux, obs *ObservabilityServer, httpBridgeURL string) {
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
		observability.WriteProtoJSON(w, caps)
	})

	observability.MountTraceBridge(mux, obs.Store())
}
