// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package controlapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/foundation/pki"
	foundationshared "github.com/rimsky-ai/rimsky-core/lib/foundation/shared"
	"github.com/rimsky-ai/rimsky-core/lib/protocols/enroll"
)

type EnrollDeps struct {
	CA      *pki.CA
	LeafTTL time.Duration
	Clock   foundationshared.Clock
}

func registerEnrollRoutes(r chi.Router, deps AppDeps) {
	if deps.Enroll == nil || deps.Enroll.CA == nil {
		return
	}
	r.Post("/enroll", deps.AuthState.gateByAction("service:enroll", handleEnroll(deps)))
}

// @concept: peer-auth
func registerCARootRoute(r chi.Router, deps AppDeps) {
	if deps.Enroll == nil || deps.Enroll.CA == nil {
		return
	}
	r.Get("/ca-root", handleCARoot(deps))
}

// @concept: peer-auth
// @decision: enroll-token-is-api-key
func handleCARoot(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		pem := deps.Enroll.CA.CertPEM()
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pem)
	}
}

// @decision: enroll-token-is-api-key
func handleEnroll(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		type req struct {
			Label string `json:"label,omitempty"`
		}
		var body req
		if r.Body != nil && r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				badRequest(w, "invalid JSON: "+err.Error())
				return
			}
		}
		ident, ok := IdentityFromContextOK(r.Context())
		if !ok || ident.KeyID == nil {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error": "enrollment requires an authenticated api-key principal",
			})
			return
		}
		ttl := deps.Enroll.LeafTTL
		if ttl <= 0 {
			ttl = pki.LeafTTL
		}
		clock := deps.Enroll.Clock
		if clock == nil {
			clock = foundationshared.SystemClock{}
		}
		now := clock.Now()
		issued, err := deps.Enroll.CA.IssueLeaf(ident.KeyID.String(), now, ttl)
		if err != nil {
			writeError(w, err)
			return
		}
		if deps.Logger != nil {
			deps.Logger.Info("service enrolled",
				"label", body.Label,
				"principal", ident.KeyID.String(),
				"not_after", issued.NotAfter.Format(time.RFC3339))
		}
		writeJSON(w, http.StatusOK, enroll.Response{
			CertPEM:   string(issued.CertPEM),
			KeyPEM:    string(issued.KeyPEM),
			CARootPEM: string(deps.Enroll.CA.CertPEM()),
			NotAfter:  issued.NotAfter,
		})
	}
}
