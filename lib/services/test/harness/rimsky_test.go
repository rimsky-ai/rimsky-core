// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package harness

import (
	"context"
	"net/http"
	"testing"
)

func TestBringUpRimsky_HealthGreen(t *testing.T) {
	ep := BringUpRimsky(context.Background(), t)
	status, body := ep.GetJSON(t, "/v1/health", "")
	if status != http.StatusOK {
		t.Fatalf("/v1/health = %d, want 200; body=%s", status, string(body))
	}
}
