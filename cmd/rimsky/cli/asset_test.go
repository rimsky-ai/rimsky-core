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
	"time"
)

func TestClient_ListAssets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/instances/abc/assets" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"assets": []map[string]any{
				{
					"alias":          "loader.fs",
					"claim_id":       "claim-1",
					"producer_name":  "fs",
					"node_type":      "loader",
					"version_id":     "v1",
					"claimed_at":     time.Now().UTC().Format(time.RFC3339),
					"holder_node_id": "node-1",
					"state":          "committed",
					"lifetime":       "durable",
				},
			},
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	resp, err := c.ListAssets(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Assets) != 1 || resp.Assets[0].Alias != "loader.fs" {
		t.Errorf("assets: %+v", resp.Assets)
	}
	if resp.Assets[0].State != "committed" {
		t.Errorf("state: %q (want committed)", resp.Assets[0].State)
	}
	if resp.Assets[0].Lifetime != "durable" {
		t.Errorf("lifetime: %q (want durable)", resp.Assets[0].Lifetime)
	}
}

func TestClient_DeleteAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method: %s", r.Method)
		}
		if r.URL.Path != "/v1/instances/abc/assets/loader.fs" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": true})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	if err := c.DeleteAsset(context.Background(), "abc", "loader.fs"); err != nil {
		t.Fatal(err)
	}
}

func TestClient_GetClaimAncestors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/lineage/claims/claim-1/ancestors" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("depth") != "5" {
			t.Errorf("depth missing: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ancestors": []map[string]any{
				{"id": "l1", "record_kind": "claim_terminal", "instance_id": "abc", "frame_id": "f", "record": map[string]any{}, "outcome": "committed"},
			},
			"depth": 5,
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	resp, err := c.GetClaimAncestors(context.Background(), "claim-1", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Ancestors) != 1 {
		t.Errorf("ancestors: %+v", resp.Ancestors)
	}
}
