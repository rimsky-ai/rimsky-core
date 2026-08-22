// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package plumbline

import (
	"net/http"
	"sort"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/rimsky-ai/rimsky-core/lib/control/observability"
)

// @concept: cascade-graph
func TestCascadeGraphSurfaceRegistersReadsOnly(t *testing.T) {
	r := chi.NewRouter()
	observability.Routes(r, observability.Deps{})

	var writes []string
	var routes []string
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, method+" "+route)
		if method != http.MethodGet && method != http.MethodHead {
			writes = append(writes, method+" "+route)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the cascade-graph router: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("the cascade-graph router registered no route; the check would pass over an empty surface")
	}
	sort.Strings(writes)
	if len(writes) > 0 {
		t.Fatalf("%d of %d handlers on the cascade-graph read surface accept a write method; "+
			"no handler in this family mutates anything, so every route must be a read: %v",
			len(writes), len(routes), writes)
	}
	t.Logf("checked all %d routes observability.Routes registers; every one is a read", len(routes))
}
