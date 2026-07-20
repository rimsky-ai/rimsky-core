// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func parkedNodesHandler(t *testing.T, rows []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/diagnostics/parked-nodes" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"parked_nodes": rows})
	}))
}

func TestRunParkedList_NoFilters(t *testing.T) {
	now := time.Now().UTC()
	srv := parkedNodesHandler(t, []map[string]any{
		{"instance_id": "i1", "node_id": "n1", "parked_at": now.Format(time.RFC3339)},
		{"instance_id": "i2", "node_id": "n2", "parked_at": now.Format(time.RFC3339)},
	})
	defer srv.Close()

	var code int
	out := captureStdout(t, func() {
		code = RunParkedList(context.Background(), []string{"--endpoint", srv.URL})
	})
	if code != 0 {
		t.Fatalf("exit %d, output: %s", code, out)
	}
	if !strings.Contains(out, "i1") || !strings.Contains(out, "i2") {
		t.Fatalf("expected both parked rows in output, got: %q", out)
	}
}

func TestRunParkedList_OlderThanFiltersClientSide(t *testing.T) {
	old := time.Now().UTC().Add(-2 * time.Hour)
	recent := time.Now().UTC().Add(-1 * time.Minute)
	srv := parkedNodesHandler(t, []map[string]any{
		{"instance_id": "i-old", "node_id": "n1", "parked_at": old.Format(time.RFC3339)},
		{"instance_id": "i-recent", "node_id": "n2", "parked_at": recent.Format(time.RFC3339)},
	})
	defer srv.Close()

	var code int
	out := captureStdout(t, func() {
		code = RunParkedList(context.Background(), []string{"--endpoint", srv.URL, "--older-than", "1h"})
	})
	if code != 0 {
		t.Fatalf("exit %d, output: %s", code, out)
	}
	if !strings.Contains(out, "i-old") {
		t.Errorf("row parked longer than the cutoff should be included, got: %q", out)
	}
	if strings.Contains(out, "i-recent") {
		t.Errorf("row parked more recently than the cutoff should be excluded, got: %q", out)
	}
}

func TestRunParkedList_InstanceFiltersClientSide(t *testing.T) {
	now := time.Now().UTC()
	srv := parkedNodesHandler(t, []map[string]any{
		{"instance_id": "i1", "node_id": "n1", "parked_at": now.Format(time.RFC3339)},
		{"instance_id": "i2", "node_id": "n2", "parked_at": now.Format(time.RFC3339)},
	})
	defer srv.Close()

	var code int
	out := captureStdout(t, func() {
		code = RunParkedList(context.Background(), []string{"--endpoint", srv.URL, "--instance", "i2"})
	})
	if code != 0 {
		t.Fatalf("exit %d, output: %s", code, out)
	}
	if strings.Contains(out, "i1") {
		t.Errorf("--instance filter should have excluded i1, got: %q", out)
	}
	if !strings.Contains(out, "i2") {
		t.Errorf("--instance filter should have kept i2, got: %q", out)
	}
}

func TestRunParkedList_JSONIncludesResumeAt(t *testing.T) {
	now := time.Now().UTC()
	resumeAt := now.Add(5 * time.Minute)
	srv := parkedNodesHandler(t, []map[string]any{
		{"instance_id": "i1", "node_id": "n1", "parked_at": now.Format(time.RFC3339), "resume_at": resumeAt.Format(time.RFC3339)},
	})
	defer srv.Close()

	var code int
	out := captureStdout(t, func() {
		code = RunParkedList(context.Background(), []string{"--endpoint", srv.URL, "--output", "json"})
	})
	if code != 0 {
		t.Fatalf("exit %d, output: %s", code, out)
	}
	var rows []ParkedNodeEntry
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("decode: %v; output: %s", err, out)
	}
	if len(rows) != 1 || rows[0].ResumeAt == nil {
		t.Fatalf("expected one row with resume_at set, got: %+v", rows)
	}
}
