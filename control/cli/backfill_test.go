// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_CreateBackfill(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: %s", r.Method)
		}
		if r.URL.Path != "/instances/abc/backfills" {
			t.Errorf("path: %s", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(raw, &got)
		if got["target_node"] != "loader" {
			t.Errorf("target_node: %v", got["target_node"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message_id":            "msg-1",
			"backfill_operation_id": "op-1",
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	resp, err := c.CreateBackfill(context.Background(), "abc", CreateBackfillRequest{TargetNode: "loader"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.BackfillOperationID != "op-1" {
		t.Errorf("op_id: %s", resp.BackfillOperationID)
	}
}

func TestClient_ListBackfills(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/instances/abc/backfills" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"backfills": []map[string]any{
				{"operation_id": "op-1", "message_id": "msg-1", "target_node": "loader"},
			},
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	resp, err := c.ListBackfills(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Backfills) != 1 || resp.Backfills[0].OperationID != "op-1" {
		t.Errorf("backfills: %+v", resp.Backfills)
	}
}

func TestClient_CancelBackfill(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backfills/op-1/cancel" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cancelled":       true,
			"messages_voided": 1,
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	out, err := c.CancelBackfill(context.Background(), "op-1")
	if err != nil {
		t.Fatal(err)
	}
	if out["cancelled"] != true {
		t.Errorf("cancelled: %v", out)
	}
}

func TestRunBackfillCreate_RangeShorthand(t *testing.T) {
	got := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got = string(raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message_id":            "m",
			"backfill_operation_id": "op",
		})
	}))
	defer srv.Close()
	t.Setenv("RIMSKY_CONTROL_API", srv.URL)
	code := RunBackfillCreate(context.Background(), []string{
		"--instance", "11111111-1111-1111-1111-111111111111",
		"--node", "loader",
		"--range", "2024-01-01..2024-09-30",
	})
	if code != 0 {
		t.Fatalf("exit: %d, body=%s", code, got)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(got), &body)
	override, _ := body["partition_request_override"].(map[string]any)
	if override == nil {
		t.Fatalf("missing partition_request_override: %s", got)
	}
	dr, _ := override["date_range"].(map[string]any)
	if dr["start"] != "2024-01-01" || dr["end"] != "2024-09-30" {
		t.Errorf("date_range: %+v", dr)
	}
}
