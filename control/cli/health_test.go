// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0. See LICENSE.apache at the
// repo root, or http://www.apache.org/licenses/LICENSE-2.0.

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
		if r.URL.Path != "/health" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":      "ok",
			"supervisors": []map[string]any{{"id": "sup1", "concurrency": 4, "active_node_count": 0, "last_heartbeat_at": "2026-05-02T00:00:00Z", "accepted_executors": []string{}}},
			"node_counts": map[string]int{"fresh": 0},
		})
	}))
}

func TestRunHealth_Human(t *testing.T) {
	srv := fakeHealthServer(t)
	defer srv.Close()
	t.Setenv("RIMSKY_CONTROL_API", srv.URL)
	t.Setenv("RIMSKY_CONTEXT", "")
	t.Setenv("HOME", t.TempDir())
	if got := RunHealth(context.Background(), nil); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunHealth_JSON(t *testing.T) {
	srv := fakeHealthServer(t)
	defer srv.Close()
	t.Setenv("RIMSKY_CONTROL_API", srv.URL)
	t.Setenv("RIMSKY_CONTEXT", "")
	t.Setenv("HOME", t.TempDir())
	if got := RunHealth(context.Background(), []string{"-o", "json"}); got != 0 {
		t.Errorf("exit %d", got)
	}
}

func TestRunHealth_NoEndpoint(t *testing.T) {
	t.Setenv("RIMSKY_CONTROL_API", "")
	t.Setenv("RIMSKY_CONTEXT", "")
	t.Setenv("HOME", t.TempDir())
	if got := RunHealth(context.Background(), nil); got != 2 {
		t.Errorf("want 2, got %d", got)
	}
}
