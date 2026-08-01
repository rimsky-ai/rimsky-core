// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_PruneLineage(t *testing.T) {
	got := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/lineage/prune" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method: %s", r.Method)
		}
		raw, _ := io.ReadAll(r.Body)
		got = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"deleted": 42,
			"before":  "2025-01-01T00:00:00Z",
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	out, err := c.PruneLineage(context.Background(), "2025-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if int(out["deleted"].(float64)) != 42 {
		t.Errorf("deleted: %v", out)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(got), &body)
	if body["before"] != "2025-01-01T00:00:00Z" {
		t.Errorf("body.before: %v", body)
	}
}

func TestRunLineagePrune_RequiresBefore(t *testing.T) {
	if code := RunLineagePrune(context.Background(), []string{}); code != 2 {
		t.Errorf("exit: %d", code)
	}
}
