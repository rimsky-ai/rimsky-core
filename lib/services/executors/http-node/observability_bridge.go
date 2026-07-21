// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package httpnode

import (
	"net/http"

	"github.com/rimsky-ai/rimsky-core/lib/services/executors/internal/observability"
)

func MountObservabilityBridge(mux *http.ServeMux, obs *ObservabilityServer) {
	mux.HandleFunc("/observability/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		observability.WriteProtoJSON(w, obs.CapabilitiesPayload())
	})

	observability.MountTraceBridge(mux, obs.Store())
}
