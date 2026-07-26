// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fakeHealthServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":      "ok",
			"supervisors": []map[string]any{{"id": "sup1", "concurrency": 4, "active_node_count": 0}},
			"node_counts": map[string]int{"fresh": 0},
		})
	}))
}

func TestRunHealth_Human(t *testing.T) {
	srv := fakeHealthServer(t)
	defer srv.Close()
	t.Setenv("RIMSKY_CONTROL_API_URL", srv.URL)
	t.Setenv("RIMSKY_CONTEXT", "")
	t.Setenv("HOME", t.TempDir())
	if got := RunHealth(context.Background(), nil); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunHealth_JSON(t *testing.T) {
	srv := fakeHealthServer(t)
	defer srv.Close()
	t.Setenv("RIMSKY_CONTROL_API_URL", srv.URL)
	t.Setenv("RIMSKY_CONTEXT", "")
	t.Setenv("HOME", t.TempDir())
	if got := RunHealth(context.Background(), []string{"-o", "json"}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunHealth_NoEndpoint(t *testing.T) {
	t.Setenv("RIMSKY_CONTROL_API_URL", "")
	t.Setenv("RIMSKY_CONTEXT", "")
	t.Setenv("HOME", t.TempDir())
	if got := RunHealth(context.Background(), nil); got != 2 {
		t.Errorf("want 2, got %d", got)
	}
}
