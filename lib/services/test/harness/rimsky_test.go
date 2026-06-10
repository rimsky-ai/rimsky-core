// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package harness

import (
	"context"
	"net/http"
	"testing"
)

// TestBringUpRimsky_HealthGreen exercises the minimum-viable harness
// bring-up: rimsky/all + postgres on a shared network, control-api
// answers `GET /v1/health` 200. Used as a fast sanity check that the
// `rimsky-all-in-one:latest` image and the rendered rimsky.yml are mutually
// compatible. The downstream tests in test/smoke and test/scenarios
// depend on this baseline.
func TestBringUpRimsky_HealthGreen(t *testing.T) {
	ep := BringUpRimsky(context.Background(), t)
	status, body := ep.GetJSON(t, "/v1/health", "")
	if status != http.StatusOK {
		t.Fatalf("/v1/health = %d, want 200; body=%s", status, string(body))
	}
}
